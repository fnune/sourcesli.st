package etl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DumpSource restores the published UDD dump.
//
// It is the only type in the repository that contacts Debian, so the etiquette
// rules live here and nowhere else: one conditional request per run, an
// identifying User-Agent, and a body transfer only when upstream has actually
// republished.
//
// The dump cannot be restored straight into a schema of our own database:
// pg_restore has no schema remapping, and the dump names its tables in public,
// which is where our schema already lives. So it goes into a scratch database
// and is exposed through postgres_fdw as the udd_raw schema, which is what lets
// the transform queries read it with ordinary SQL.
type DumpSource struct {
	client        *http.Client
	pool          *pgxpool.Pool
	url           string
	cacheDir      string
	userAgent     string
	admin         ConnectionSettings
	scratchDB     string
	scratchSchema string
	tables        []string
	logger        *slog.Logger
}

// ConnectionSettings is what the Postgres command line tools need. pg_restore,
// createdb, and dropdb are processes rather than library calls, so they cannot
// share the pool the rest of the package uses.
type ConnectionSettings struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// DumpSourceOptions configures a DumpSource. Every field is required; there are
// no defaults, because a default upstream URL or a default identification
// string is exactly the kind of thing that ends up shipped by accident.
type DumpSourceOptions struct {
	Client    *http.Client
	Pool      *pgxpool.Pool
	URL       string
	CacheDir  string
	UserAgent string
	Admin     ConnectionSettings
	ScratchDB string
	// ScratchSchema declares the tables the restore pours into. See
	// recreateScratchDatabase for why we do not use UDD's own DDL.
	ScratchSchema string
	Tables        []string
	Logger        *slog.Logger
}

// NewDumpSource validates the options and returns a source.
func NewDumpSource(options DumpSourceOptions) (*DumpSource, error) {
	switch {
	case options.Client == nil:
		return nil, errors.New("etl: an HTTP client is required")
	case options.Pool == nil:
		return nil, errors.New("etl: a connection pool for the application database is required")
	case options.URL == "":
		return nil, errors.New("etl: the dump URL is required")
	case options.CacheDir == "":
		return nil, errors.New("etl: a cache directory is required")
	case options.UserAgent == "":
		return nil, errors.New("etl: a User-Agent identifying this project is required")
	case options.ScratchDB == "":
		return nil, errors.New("etl: a scratch database name is required")
	case options.ScratchSchema == "":
		return nil, errors.New("etl: the scratch schema is required")
	case len(options.Tables) == 0:
		return nil, errors.New("etl: at least one table to restore is required")
	case options.Logger == nil:
		return nil, errors.New("etl: a logger is required")
	}

	if !safeIdentifier.MatchString(options.ScratchDB) {
		return nil, fmt.Errorf("etl: scratch database name %q is not a plain identifier", options.ScratchDB)
	}
	for _, table := range options.Tables {
		if !safeIdentifier.MatchString(table) {
			return nil, fmt.Errorf("etl: table name %q is not a plain identifier", table)
		}
	}

	return &DumpSource{
		client:        options.Client,
		pool:          options.Pool,
		url:           options.URL,
		cacheDir:      options.CacheDir,
		userAgent:     options.UserAgent,
		admin:         options.Admin,
		scratchDB:     options.ScratchDB,
		scratchSchema: options.ScratchSchema,
		tables:        options.Tables,
		logger:        options.Logger,
	}, nil
}

// Name identifies this source in logs and in sync_runs.
func (s *DumpSource) Name() string { return "udd-dump" }

// Restore fetches the dump if it has changed and loads it into the scratch
// database. It returns ErrUnchanged without transferring a body when upstream
// still holds the publication described by state.
func (s *DumpSource) Restore(ctx context.Context, state UpstreamState) (Snapshot, error) {
	snapshot, path, err := s.fetch(ctx, state)
	if err != nil {
		return Snapshot{}, err
	}

	if err := s.recreateScratchDatabase(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("preparing the scratch database: %w", err)
	}
	if err := s.restoreInto(ctx, path); err != nil {
		return Snapshot{}, fmt.Errorf("restoring the dump: %w", err)
	}
	if err := exposeScratchSchema(ctx, s.pool, s.admin, s.scratchDB, s.tables); err != nil {
		return Snapshot{}, err
	}

	return snapshot, nil
}

// fetch performs the one conditional request a run is allowed.
func (s *DumpSource) fetch(ctx context.Context, state UpstreamState) (Snapshot, string, error) {
	if err := os.MkdirAll(s.cacheDir, 0o750); err != nil {
		return Snapshot{}, "", fmt.Errorf("creating the cache directory: %w", err)
	}
	cached := filepath.Join(s.cacheDir, "udd.dump")

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("building the dump request: %w", err)
	}
	request.Header.Set("User-Agent", s.userAgent)
	if state.ETag != "" {
		request.Header.Set("If-None-Match", state.ETag)
	}
	if state.LastModified != "" {
		request.Header.Set("If-Modified-Since", state.LastModified)
	}

	s.logger.InfoContext(ctx, "requesting the dump",
		slog.String("url", s.url),
		slog.String("if_none_match", state.ETag))

	response, err := s.client.Do(request)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("requesting the dump: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			s.logger.WarnContext(ctx, "closing the dump response failed", slog.Any("error", err))
		}
	}()

	switch response.StatusCode {
	case http.StatusNotModified:
		// Nothing is restored on a 304: the previously published data already
		// lives in Postgres, so the cached file is not needed and its absence
		// (an ephemeral cache directory, say) must not fail the run.
		return Snapshot{}, "", ErrUnchanged
	case http.StatusOK:
	default:
		return Snapshot{}, "", fmt.Errorf("requesting the dump: upstream answered %s", response.Status)
	}

	written, err := writeAtomically(cached, response.Body)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("saving the dump: %w", err)
	}

	s.logger.InfoContext(ctx, "dump downloaded",
		slog.Int64("bytes", written),
		slog.String("etag", response.Header.Get("ETag")))

	return Snapshot{
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
	}, cached, nil
}

// writeAtomically streams into a temporary file and renames it into place, so
// an interrupted download can never be mistaken for a complete one.
func writeAtomically(destination string, source io.Reader) (int64, error) {
	temporary, err := os.CreateTemp(filepath.Dir(destination), filepath.Base(destination)+".partial-*")
	if err != nil {
		return 0, err
	}
	name := temporary.Name()

	written, copyErr := io.Copy(temporary, source)
	closeErr := temporary.Close()

	if err := errors.Join(copyErr, closeErr); err != nil {
		return 0, errors.Join(err, os.Remove(name))
	}
	if err := os.Rename(name, destination); err != nil {
		return 0, errors.Join(err, os.Remove(name))
	}
	return written, nil
}

// recreateScratchDatabase rebuilds the scratch database and declares the tables
// the restore will pour data into.
//
// We declare them rather than letting pg_restore create them from UDD's own
// DDL, because UDD types its version columns as `debversion` and its severity
// column as an enum. Neither definition travels with a selective
// `pg_restore --table`, so restoring upstream's schema fails on every table.
// Declaring wider types here also means an upstream type change cannot break
// the restore, only the transform that reads it.
func (s *DumpSource) recreateScratchDatabase(ctx context.Context) error {
	if err := s.runTool(ctx, toolDropDB, "--if-exists", s.scratchDB); err != nil {
		return err
	}
	if err := s.runTool(ctx, toolCreateDB, s.scratchDB); err != nil {
		return err
	}
	return s.runToolWithInput(
		ctx, toolPsql, s.scratchSchema,
		"--dbname="+s.scratchDB,
		"--quiet",
		"--no-psqlrc",
		"--set=ON_ERROR_STOP=1",
		"--file=-",
	)
}

func (s *DumpSource) restoreInto(ctx context.Context, dumpPath string) error {
	arguments := []string{
		"--dbname=" + s.scratchDB,
		"--data-only",
		"--no-owner",
		"--no-privileges",
		"--no-comments",
		"--jobs=4",
	}
	for _, table := range s.tables {
		arguments = append(arguments, "--table="+table)
	}
	arguments = append(arguments, dumpPath)

	s.logger.InfoContext(ctx, "restoring the dump",
		slog.String("database", s.scratchDB),
		slog.String("tables", strings.Join(s.tables, ",")))

	return s.runTool(ctx, toolPgRestore, arguments...)
}

// postgresTool names an executable this package is allowed to run. A closed set
// of constants rather than a string, so no code path can introduce a new one.
type postgresTool string

// The Postgres command line tools the extract step shells out to.
const (
	toolDropDB    postgresTool = "dropdb"
	toolCreateDB  postgresTool = "createdb"
	toolPgRestore postgresTool = "pg_restore"
	toolPsql      postgresTool = "psql"
)

// runTool invokes a Postgres command line tool with connection settings passed
// through the environment, so a password never appears in a process listing.
func (s *DumpSource) runTool(ctx context.Context, tool postgresTool, arguments ...string) error {
	return s.runToolWithInput(ctx, tool, "", arguments...)
}

// runToolWithInput is runTool with something on standard input, which is how
// SQL reaches psql without leaving a temporary file for somebody to clean up.
//
// constants, and that every interpolated argument is either connection
// configuration or a name already checked against safeIdentifier.
//
//nolint:gosec // G204 cannot see that the executable is one of four package
func (s *DumpSource) runToolWithInput(
	ctx context.Context,
	tool postgresTool,
	input string,
	arguments ...string,
) error {
	name := string(tool)
	command := exec.CommandContext(ctx, name, append([]string{
		"--host=" + s.admin.Host,
		"--port=" + s.admin.Port,
		"--username=" + s.admin.User,
	}, arguments...)...)

	if input != "" {
		command.Stdin = strings.NewReader(input)
	}

	command.Env = append(
		os.Environ(),
		"PGPASSWORD="+s.admin.Password,
		"PGAPPNAME="+s.userAgent,
		"PGCONNECT_TIMEOUT=10",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// DefaultHTTPClient returns the client the ingest binary uses. The timeout is
// generous because the body is measured in gigabytes, and redirects are
// followed because the dump is served from a host that may move.
func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Hour}
}

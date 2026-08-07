// Command ingest runs one ETL pass: fetch an upstream snapshot, transform it
// into our schema, and publish it.
//
// It is a job rather than a service. Running it on a schedule is the scheduler's
// job, which keeps the cadence rule visible in configuration rather than buried
// in a loop inside a binary nobody reads.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	sourceslist "github.com/fnune/sourcesli.st"
	"github.com/fnune/sourcesli.st/internal/etl"
	"github.com/fnune/sourcesli.st/internal/store"
)

// version is stamped at build time. It appears in the string we identify
// ourselves with to Debian, so an operator can tell which build is calling.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.ErrorContext(ctx, "ingest failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	config, err := loadConfig()
	if err != nil {
		return err
	}

	pool, err := store.Open(ctx, config.databaseURL, config.userAgent)
	if err != nil {
		return err
	}
	defer pool.Close()

	migrations, err := subFS("db/migrations")
	if err != nil {
		return err
	}
	if err := store.Migrate(ctx, pool, migrations, logger); err != nil {
		return fmt.Errorf("migrating the database: %w", err)
	}

	seed, err := subFS("db/seed")
	if err != nil {
		return err
	}

	host, port, user, password, _, err := store.ConnectionSettingsFrom(config.databaseURL)
	if err != nil {
		return err
	}

	tables, err := readTableList()
	if err != nil {
		return err
	}

	scratchSchemaBytes, err := sourceslist.SQL.ReadFile("db/etl/extract/scratch_schema.sql")
	if err != nil {
		return fmt.Errorf("reading the scratch schema: %w", err)
	}
	scratchSchema := string(scratchSchemaBytes)

	source, err := etl.NewDumpSource(etl.DumpSourceOptions{
		Client:    etl.DefaultHTTPClient(),
		Pool:      pool,
		URL:       config.dumpURL,
		CacheDir:  config.cacheDir,
		UserAgent: config.userAgent,
		Admin: etl.ConnectionSettings{
			Host:     host,
			Port:     port,
			User:     user,
			Password: password,
		},
		ScratchDB:     config.scratchDB,
		ScratchSchema: scratchSchema,
		Tables:        tables,
		Logger:        logger,
	})
	if err != nil {
		return err
	}

	transforms, err := subFS("db/etl/transform")
	if err != nil {
		return err
	}

	loader := etl.NewLoader(pool, transforms, seed, logger)
	pipeline := etl.NewPipeline(pool, source, loader, etl.SystemClock, logger)

	result, err := pipeline.Run(ctx)
	if err != nil {
		return err
	}

	// The chooser falls back to compiled-in versions, so failing to read a
	// point release degrades one page rather than the whole sync.
	if err := etl.SyncISOVersions(
		ctx, pool, etl.ISOVersionsHTTPClient(), config.userAgent, etl.SystemClock, logger,
	); err != nil {
		logger.WarnContext(ctx, "refreshing the ISO versions failed", slog.Any("error", err))
	}

	// A missing size costs one tile a rounded figure, never a failed sync.
	if err := etl.SyncISOSizes(
		ctx, pool, etl.ISOVersionsHTTPClient(), config.userAgent, etl.SystemClock, logger,
	); err != nil {
		logger.WarnContext(ctx, "refreshing the ISO sizes failed", slog.Any("error", err))
	}

	// A missing thumbnail costs a grey box on the front page, never a failed
	// sync.
	if err := etl.SyncScreenshots(
		ctx, pool, etl.ISOVersionsHTTPClient(), config.userAgent, etl.SystemClock, logger,
	); err != nil {
		logger.WarnContext(ctx, "refreshing the screenshots failed", slog.Any("error", err))
	}

	logger.InfoContext(ctx, "ingest finished",
		slog.String("outcome", string(result.Outcome)),
		slog.Int64("rows", result.RowsWritten))
	return nil
}

type config struct {
	databaseURL string
	dumpURL     string
	cacheDir    string
	scratchDB   string
	userAgent   string
}

// loadConfig reads configuration from the environment and fails when anything
// required is missing, rather than defaulting its way into a half-configured
// run against the wrong database.
func loadConfig() (config, error) {
	loaded := config{
		databaseURL: os.Getenv("DATABASE_URL"),
		dumpURL:     os.Getenv("UDD_DUMP_URL"),
		cacheDir:    os.Getenv("UDD_CACHE_DIR"),
		scratchDB:   os.Getenv("UDD_SCRATCH_DATABASE"),
	}

	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":         loaded.databaseURL,
		"UDD_DUMP_URL":         loaded.dumpURL,
		"UDD_CACHE_DIR":        loaded.cacheDir,
		"UDD_SCRATCH_DATABASE": loaded.scratchDB,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	loaded.userAgent = fmt.Sprintf("sourcesli.st/%s (+https://github.com/fnune/sourcesli.st)", version)
	return loaded, nil
}

// readTableList reads the UDD tables the extract step restores. Keeping it in a
// file rather than a Go slice means the list a reviewer sees in the diff is the
// same one pg_restore is handed.
func readTableList() ([]string, error) {
	content, err := sourceslist.SQL.ReadFile("db/etl/extract/tables.txt")
	if err != nil {
		return nil, fmt.Errorf("reading the table list: %w", err)
	}

	var tables []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tables = append(tables, line)
	}
	if len(tables) == 0 {
		return nil, errors.New("the table list is empty")
	}
	return tables, nil
}

// subFS narrows the embedded filesystem to one directory, so a caller receives
// exactly the SQL it is meant to run and cannot reach a sibling directory by
// naming a path.
func subFS(directory string) (fs.FS, error) {
	narrowed, err := fs.Sub(sourceslist.SQL, directory)
	if err != nil {
		return nil, fmt.Errorf("reading embedded %s: %w", directory, err)
	}
	return narrowed, nil
}

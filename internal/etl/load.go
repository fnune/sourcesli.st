package etl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stagingSuffix names the table a batch is built in before it is published.
const stagingSuffix = "__new"

// retiredSuffix names the outgoing table during the moment of the swap.
const retiredSuffix = "__old"

// replaceableTables are rebuilt wholesale by every run, in the order the
// transform queries expect: package_search reads the others, so it comes last.
//
// This slice is the only source of table names the loader will act on. Names
// reach SQL by interpolation because Postgres has no parameter form for an
// identifier, so they must never come from configuration, a query result, or
// anything else a person could steer.
var replaceableTables = []string{
	"source_packages",
	"binary_packages",
	"bugs",
	"bug_tags",
	"popcon",
	"package_search",
}

// safeIdentifier matches the table names we generate. Applied to every name
// before it is interpolated, so a future edit to replaceableTables cannot turn
// into an injection by being careless rather than malicious.
var safeIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// errNoRows lets the pipeline treat an absent upstream_state row as an empty
// state without importing pgx there.
var errNoRows = pgx.ErrNoRows

// Loader publishes a batch: it stages the new tables, runs the transform
// queries into them, and swaps them into place.
//
// The swap is a rename inside one transaction. Readers block only for the
// rename itself rather than for the whole rebuild, and a failure before the
// swap leaves the previously published data untouched.
type Loader struct {
	pool       *pgxpool.Pool
	transforms fs.FS
	seed       fs.FS
	logger     *slog.Logger
}

// NewLoader wires a loader. transforms and seed are filesystems rather than
// paths so that a binary carries its own SQL and a test can supply its own.
func NewLoader(pool *pgxpool.Pool, transforms, seed fs.FS, logger *slog.Logger) *Loader {
	return &Loader{pool: pool, transforms: transforms, seed: seed, logger: logger}
}

// Load builds and publishes one batch, returning the number of rows published.
func (l *Loader) Load(ctx context.Context) (int64, error) {
	for _, table := range replaceableTables {
		if !safeIdentifier.MatchString(table) {
			return 0, fmt.Errorf("refusing to build a query around table name %q", table)
		}
	}

	if err := l.applySeed(ctx); err != nil {
		return 0, fmt.Errorf("applying seed data: %w", err)
	}

	staged, err := l.stage(ctx)
	if err != nil {
		return 0, fmt.Errorf("staging tables: %w", err)
	}

	if err := l.transform(ctx); err != nil {
		return 0, fmt.Errorf("running transform queries: %w", err)
	}

	if err := l.restoreIndexes(ctx, staged); err != nil {
		return 0, fmt.Errorf("rebuilding indexes on the staged tables: %w", err)
	}

	rows, err := l.swap(ctx)
	if err != nil {
		return 0, fmt.Errorf("swapping the staged tables into place: %w", err)
	}

	return rows, nil
}

// applySeed loads the data we own rather than derive, which the transform
// queries join against. Suites in particular must exist before any transform
// runs, because a package whose release matches no suite is dropped.
func (l *Loader) applySeed(ctx context.Context) error {
	statements, err := readSQLDir(l.seed)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := l.pool.Exec(ctx, statement.sql); err != nil {
			return fmt.Errorf("%s: %w", statement.name, err)
		}
	}
	return nil
}

// stagedIndex is an index that was dropped for the duration of the load, and
// the statement that recreates it.
type stagedIndex struct {
	table      string
	definition string
}

// stage creates an empty `<table>__new` for every replaceable table.
//
// The tables are created with every index, then all indexes except the one
// backing the primary key are dropped for the duration of the insert. The
// primary key has to survive because the transform queries rely on ON CONFLICT,
// which requires a unique index to conflict against. The rest are rebuilt
// afterwards, since building a GIN index once over a finished table costs far
// less than maintaining it across several million inserts.
func (l *Loader) stage(ctx context.Context) ([]stagedIndex, error) {
	var dropped []stagedIndex

	for _, table := range replaceableTables {
		staging := table + stagingSuffix

		if _, err := l.pool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s CASCADE`, staging)); err != nil {
			return nil, fmt.Errorf("dropping a leftover %s: %w", staging, err)
		}
		if _, err := l.pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (LIKE %s INCLUDING ALL)`, staging, table)); err != nil {
			return nil, fmt.Errorf("creating %s: %w", staging, err)
		}

		indexes, err := l.secondaryIndexes(ctx, staging)
		if err != nil {
			return nil, fmt.Errorf("listing indexes on %s: %w", staging, err)
		}
		for _, index := range indexes {
			if _, err := l.pool.Exec(ctx, fmt.Sprintf(`DROP INDEX %s`, index.name)); err != nil {
				return nil, fmt.Errorf("dropping index %s: %w", index.name, err)
			}
			dropped = append(dropped, stagedIndex{table: staging, definition: index.definition})
		}
	}

	return dropped, nil
}

type indexDefinition struct {
	name       string
	definition string
}

// secondaryIndexes lists the indexes on a table that do not back a constraint.
// Constraint-backed indexes cannot be dropped independently of the constraint,
// and the primary key among them is the one the transforms need.
func (l *Loader) secondaryIndexes(ctx context.Context, table string) ([]indexDefinition, error) {
	rows, err := l.pool.Query(ctx,
		`SELECT i.indexname, i.indexdef
		 FROM pg_indexes AS i
		 JOIN pg_class AS c ON c.relname = i.indexname
		 LEFT JOIN pg_constraint AS con ON con.conindid = c.oid
		 WHERE i.schemaname = current_schema() AND i.tablename = $1 AND con.oid IS NULL`,
		table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []indexDefinition
	for rows.Next() {
		var index indexDefinition
		if err := rows.Scan(&index.name, &index.definition); err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	return indexes, rows.Err()
}

// restoreIndexes recreates what stage dropped, now that the data is in place.
func (l *Loader) restoreIndexes(ctx context.Context, dropped []stagedIndex) error {
	for _, index := range dropped {
		if _, err := l.pool.Exec(ctx, index.definition); err != nil {
			return fmt.Errorf("recreating an index on %s: %w", index.table, err)
		}
	}
	return nil
}

// transform runs every query in db/etl/transform, in filename order. The
// numeric prefixes are load-bearing: package_search reads the tables the
// earlier queries fill.
func (l *Loader) transform(ctx context.Context) error {
	statements, err := readSQLDir(l.transforms)
	if err != nil {
		return err
	}

	for _, statement := range statements {
		l.logger.InfoContext(ctx, "running transform", slog.String("query", statement.name))
		if _, err := l.pool.Exec(ctx, statement.sql); err != nil {
			return fmt.Errorf("%s: %w", statement.name, err)
		}
	}
	return nil
}

// swap publishes every staged table at once and clears the freshness overlay,
// which described versions that the incoming batch now supersedes.
func (l *Loader) swap(ctx context.Context) (int64, error) {
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			l.logger.ErrorContext(ctx, "rolling back the swap failed", slog.Any("error", err))
		}
	}()

	var total int64
	for _, table := range replaceableTables {
		staging := table + stagingSuffix
		retired := table + retiredSuffix

		var count int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, staging)).Scan(&count); err != nil {
			return 0, fmt.Errorf("counting %s: %w", staging, err)
		}
		total += count

		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, table, retired)); err != nil {
			return 0, fmt.Errorf("retiring %s: %w", table, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, staging, table)); err != nil {
			return 0, fmt.Errorf("publishing %s: %w", staging, err)
		}
	}

	if _, err := tx.Exec(ctx, `TRUNCATE fresh_binary_versions`); err != nil {
		return 0, fmt.Errorf("clearing the freshness overlay: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	for _, table := range replaceableTables {
		retired := table + retiredSuffix
		if _, err := l.pool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s CASCADE`, retired)); err != nil {
			return 0, fmt.Errorf("dropping %s after the swap: %w", retired, err)
		}
	}

	return total, nil
}

type sqlStatement struct {
	name string
	sql  string
}

// readSQLDir reads every .sql file in a filesystem, in filename order.
func readSQLDir(files fs.FS) ([]sqlStatement, error) {
	var names []string
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".sql") {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	statements := make([]sqlStatement, 0, len(names))
	for _, name := range names {
		content, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		statements = append(statements, sqlStatement{name: path.Base(name), sql: string(content)})
	}
	return statements, nil
}

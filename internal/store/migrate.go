// Package store owns the application database: applying migrations to it and,
// once sqlc generates them, running the queries the web tier reads it with.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate applies every migration the filesystem holds that the database has
// not recorded, in filename order.
//
// Migrations are forward-only and each runs inside its own transaction, so a
// failure leaves the database at the last migration that fully applied rather
// than somewhere in the middle of one.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations fs.FS, logger *slog.Logger) error {
	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
		     name        text PRIMARY KEY,
		     applied_at  timestamptz NOT NULL DEFAULT now()
		 )`); err != nil {
		return fmt.Errorf("creating the migration ledger: %w", err)
	}

	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		return fmt.Errorf("reading the migration ledger: %w", err)
	}

	names, err := migrationNames(migrations)
	if err != nil {
		return err
	}

	for _, name := range names {
		if applied[name] {
			continue
		}

		content, err := fs.ReadFile(migrations, name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}

		logger.InfoContext(ctx, "applying migration", slog.String("migration", name))
		if err := applyMigration(ctx, pool, name, string(content), logger); err != nil {
			return fmt.Errorf("applying migration %s: %w", name, err)
		}
	}

	return nil
}

func applyMigration(
	ctx context.Context,
	pool *pgxpool.Pool,
	name, statements string,
	logger *slog.Logger,
) error {
	transaction, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// After a successful commit this reports ErrTxClosed, which is the
		// expected path rather than a problem. Anything else means the rollback
		// of a failed migration did not happen, which a reader of the logs needs
		// to know about before they retry.
		if err := transaction.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			logger.ErrorContext(ctx, "rolling back a migration failed",
				slog.String("migration", name),
				slog.Any("error", err))
		}
	}()

	if _, err := transaction.Exec(ctx, statements); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx,
		`INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
		return err
	}

	return transaction.Commit(ctx)
}

func appliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}
	return applied, rows.Err()
}

func migrationNames(migrations fs.FS) ([]string, error) {
	var names []string
	err := fs.WalkDir(migrations, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".sql") {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

// Open connects to the application database and verifies the connection, so a
// misconfigured URL fails at startup rather than on the first request.
func Open(ctx context.Context, databaseURL, applicationName string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing the database URL: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = applicationName

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connecting to the database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("reaching the database: %w", err)
	}
	return pool, nil
}

// ConnectionSettingsFrom extracts what the Postgres command line tools need
// from a connection URL, so the ingest binary configures one thing rather than
// a URL and a duplicated set of host, port, and credentials.
func ConnectionSettingsFrom(databaseURL string) (host, port, user, password, database string, err error) {
	config, parseErr := pgx.ParseConfig(databaseURL)
	if parseErr != nil {
		return "", "", "", "", "", fmt.Errorf("parsing the database URL: %w", parseErr)
	}
	return config.Host,
		fmt.Sprintf("%d", config.Port),
		config.User,
		config.Password,
		config.Database,
		nil
}

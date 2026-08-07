// Command migrate brings a database up to the current schema and loads the seed
// data the transform queries join against.
//
// Separate from ingest because it needs nothing but a database: no dump, no
// scratch database, no network.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	sourceslist "github.com/fnune/sourcesli.st"
	"github.com/fnune/sourcesli.st/internal/etl"
	"github.com/fnune/sourcesli.st/internal/store"
)

const applicationName = "sourceslist-migrate"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.ErrorContext(ctx, "migrate failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("missing required environment variable: DATABASE_URL")
	}

	pool, err := store.Open(ctx, databaseURL, applicationName)
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
	if err := etl.ApplySeed(ctx, pool, seed); err != nil {
		return fmt.Errorf("applying seed data: %w", err)
	}

	logger.InfoContext(ctx, "schema and seed are up to date")
	return nil
}

func subFS(directory string) (fs.FS, error) {
	narrowed, err := fs.Sub(sourceslist.SQL, directory)
	if err != nil {
		return nil, fmt.Errorf("reading embedded %s: %w", directory, err)
	}
	return narrowed, nil
}

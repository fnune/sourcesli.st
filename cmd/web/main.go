// Command web serves the site.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sourceslist "github.com/fnune/sourcesli.st"
	"github.com/fnune/sourcesli.st/internal/store"
	"github.com/fnune/sourcesli.st/internal/store/query"
	"github.com/fnune/sourcesli.st/internal/web"
)

// version is stamped at build time and identifies this build to the database.
var version = "dev"

const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 15 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.ErrorContext(ctx, "web server failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("missing required environment variable: DATABASE_URL")
	}
	address := os.Getenv("LISTEN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}

	applicationName := fmt.Sprintf("sourcesli.st/%s (+https://github.com/fnune/sourcesli.st)", version)
	pool, err := store.Open(ctx, databaseURL, applicationName)
	if err != nil {
		return err
	}
	defer pool.Close()

	templates, err := fs.Sub(sourceslist.Web, "web/templates")
	if err != nil {
		return fmt.Errorf("reading embedded templates: %w", err)
	}
	assets, err := fs.Sub(sourceslist.Web, "web/assets")
	if err != nil {
		return fmt.Errorf("reading embedded assets: %w", err)
	}

	server, err := web.NewServer(query.New(pool), templates, assets, logger)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              address,
		Handler:           server.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	listenFailed := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "listening", slog.String("address", address))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenFailed <- err
		}
	}()

	select {
	case err := <-listenFailed:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}

	// A signal means stop accepting and let in-flight requests finish. Every
	// request is a short read, so this bound is generous rather than tuned.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	logger.InfoContext(shutdownCtx, "shutting down")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}

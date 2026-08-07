package etl

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fnune/sourcesli.st/internal/images"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncISOSizes records what cdimage serves each image at, so the download page
// can compare twelve images by size instead of repeating one rounded guess.
//
// One HEAD request per image, twelve in total, against a CDN that answers them
// from metadata: the same cost shape as reading the checksum files, and far
// below downloading anything. A failure leaves the previous row in place and
// the page falls back to its rounded figures.
func SyncISOSizes(
	ctx context.Context,
	pool *pgxpool.Pool,
	client *http.Client,
	userAgent string,
	clock Clock,
	logger *slog.Logger,
) error {
	input, err := currentInput(ctx, pool)
	if err != nil {
		return err
	}

	for _, group := range images.Catalogue(input) {
		for _, image := range group.Images() {
			size, err := fetchISOSize(ctx, client, image.DownloadURL, userAgent)
			if err != nil {
				// One missing size is one tile falling back to a rounded
				// figure, so the rest of the run continues.
				logger.WarnContext(ctx, "reading an image size failed",
					slog.String("image", image.ID), slog.Any("error", err))
				continue
			}
			if _, err := pool.Exec(ctx,
				`INSERT INTO iso_sizes (image, bytes, observed_at) VALUES ($1, $2, $3)
				 ON CONFLICT (image) DO UPDATE SET bytes = excluded.bytes, observed_at = excluded.observed_at`,
				image.ID, size, clock.Now()); err != nil {
				return fmt.Errorf("recording the size of %s: %w", image.ID, err)
			}
		}
	}
	return nil
}

// currentInput reads the point releases ingest has already observed, so the URLs
// this asks about are the ones the site is publishing.
func currentInput(ctx context.Context, pool *pgxpool.Pool) (images.Input, error) {
	var input images.Input

	rows, err := pool.Query(ctx, `SELECT channel, version FROM iso_versions`)
	if err != nil {
		return input, fmt.Errorf("reading the ISO versions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var channel, version string
		if err := rows.Scan(&channel, &version); err != nil {
			return input, err
		}
		switch channel {
		case "stable":
			input.Stable.Version = version
		case "oldstable":
			input.Oldstable.Version = version
		}
	}
	return input, rows.Err()
}

func fetchISOSize(ctx context.Context, client *http.Client, url, userAgent string) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	if err := response.Body.Close(); err != nil {
		return 0, err
	}

	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s answered %s", url, response.Status)
	}
	if response.ContentLength <= 0 {
		return 0, fmt.Errorf("%s carries no length", url)
	}
	return response.ContentLength, nil
}

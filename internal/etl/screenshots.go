package etl

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// screenshotLimit bounds how many thumbnails are mirrored: enough for the
	// front page with headroom, small enough that a full refresh is a handful
	// of tiny downloads.
	screenshotLimit = 60

	// screenshotMaxBytes bounds one thumbnail. The small images are a few tens
	// of kilobytes; anything larger is not the file we asked for.
	screenshotMaxBytes = 256 * 1024
)

// screenshotTypes are the raster formats a thumbnail may arrive in. The site
// serves the stored bytes under their stored type from its own origin, so an
// SVG, which can carry script, would turn anyone who uploads a screenshot
// into an author of the site.
var screenshotTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// SyncScreenshots mirrors thumbnails for the most used applications, so the
// site can show them without a visitor request ever reaching
// screenshots.debian.net. A thumbnail is fetched only when its source URL
// changed, and thumbnails for packages that fell out of the top set are
// pruned. Single failures are logged and skipped: a missing thumbnail costs a
// grey box, not a page.
func SyncScreenshots(
	ctx context.Context,
	pool *pgxpool.Pool,
	client *http.Client,
	userAgent string,
	clock Clock,
	logger *slog.Logger,
) error {
	rows, err := pool.Query(ctx,
		`SELECT s.package, s.small_image_url
		 FROM screenshot_sources AS s
		 WHERE EXISTS (SELECT 1 FROM package_search AS p WHERE p.name = s.package AND p.application)
		 ORDER BY (SELECT max(p.votes) FROM package_search AS p WHERE p.name = s.package) DESC
		 LIMIT $1`,
		screenshotLimit)
	if err != nil {
		return fmt.Errorf("listing screenshot sources: %w", err)
	}
	defer rows.Close()

	type source struct{ pkg, url string }
	var sources []source
	for rows.Next() {
		var s source
		if err := rows.Scan(&s.pkg, &s.url); err != nil {
			return fmt.Errorf("reading screenshot sources: %w", err)
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading screenshot sources: %w", err)
	}

	kept := make([]string, 0, len(sources))
	for _, s := range sources {
		kept = append(kept, s.pkg)

		var existing string
		err := pool.QueryRow(ctx,
			`SELECT source_url FROM package_screenshots WHERE package = $1`, s.pkg).Scan(&existing)
		if err == nil && existing == s.url {
			continue
		}

		image, contentType, err := fetchScreenshot(ctx, client, s.url, userAgent, logger)
		if err != nil {
			logger.WarnContext(ctx, "fetching a screenshot failed",
				slog.String("package", s.pkg), slog.Any("error", err))
			continue
		}

		if _, err := pool.Exec(ctx,
			`INSERT INTO package_screenshots (package, source_url, content_type, image, fetched_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (package) DO UPDATE SET
			     source_url = excluded.source_url,
			     content_type = excluded.content_type,
			     image = excluded.image,
			     fetched_at = excluded.fetched_at`,
			s.pkg, s.url, contentType, image, clock.Now()); err != nil {
			return fmt.Errorf("storing the screenshot for %s: %w", s.pkg, err)
		}
	}

	if _, err := pool.Exec(ctx,
		`DELETE FROM package_screenshots WHERE NOT (package = any($1))`, kept); err != nil {
		return fmt.Errorf("pruning retired screenshots: %w", err)
	}
	return nil
}

func fetchScreenshot(
	ctx context.Context,
	client *http.Client,
	url, userAgent string,
	logger *slog.Logger,
) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			logger.WarnContext(ctx, "closing the screenshot response failed", slog.Any("error", err))
		}
	}()

	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s answered %s", url, response.Status)
	}

	served := response.Header.Get("Content-Type")
	contentType, _, err := mime.ParseMediaType(served)
	if err != nil || !screenshotTypes[contentType] {
		return nil, "", fmt.Errorf("%s served %q, not a PNG, JPEG, or WebP image", url, served)
	}

	image, err := io.ReadAll(io.LimitReader(response.Body, screenshotMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(image) > screenshotMaxBytes {
		return nil, "", fmt.Errorf("%s exceeds %d bytes", url, screenshotMaxBytes)
	}
	return image, contentType, nil
}

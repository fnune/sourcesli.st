package etl

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// isoChannels maps a channel name to the SHA512SUMS file whose entries name
// the current point release. The files are a few kilobytes each and served
// from the cdimage CDN, so reading them once per ingest run is well inside
// the cost-shape rule.
var isoChannels = map[string]string{
	"stable":    "https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/SHA512SUMS",
	"oldstable": "https://cdimage.debian.org/cdimage/archive/latest-oldstable/i386/iso-cd/SHA512SUMS",
}

var isoVersionPattern = regexp.MustCompile(`debian-(\d+\.\d+\.\d+)-`)

// SyncISOVersions records the current point releases published on cdimage, so
// the image chooser can offer an exact download filename instead of a pattern.
// A failure here is not a failed sync: the chooser falls back to its
// compiled-in versions, and the previous rows stay in place.
func SyncISOVersions(
	ctx context.Context,
	pool *pgxpool.Pool,
	client *http.Client,
	userAgent string,
	clock Clock,
	logger *slog.Logger,
) error {
	for channel, url := range isoChannels {
		version, err := fetchISOVersion(ctx, client, url, userAgent, logger)
		if err != nil {
			return fmt.Errorf("reading the %s point release: %w", channel, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO iso_versions (channel, version, observed_at) VALUES ($1, $2, $3)
			 ON CONFLICT (channel) DO UPDATE SET version = excluded.version, observed_at = excluded.observed_at`,
			channel, version, clock.Now()); err != nil {
			return fmt.Errorf("recording the %s point release: %w", channel, err)
		}
	}
	return nil
}

func fetchISOVersion(
	ctx context.Context,
	client *http.Client,
	url, userAgent string,
	logger *slog.Logger,
) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			logger.WarnContext(ctx, "closing the checksum response failed", slog.Any("error", err))
		}
	}()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", url, response.Status)
	}

	// The checksum file is a short list of filenames; 64 KiB is orders of
	// magnitude more than it needs and bounds a misdirected response.
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", err
	}

	match := isoVersionPattern.FindSubmatch(body)
	if match == nil {
		return "", fmt.Errorf("%s names no debian-<version> file", url)
	}
	return string(match[1]), nil
}

// ISOVersionsHTTPClient bounds the checksum fetches, which are small files
// rather than the multi-gigabyte dump.
func ISOVersionsHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

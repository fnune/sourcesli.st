-- Screenshot mirroring. Hotlinking screenshots.debian.net would make every
-- visitor request a request to a Debian service, so ingest downloads a small
-- thumbnail for the top applications and the site serves its own copy.
--
-- screenshot_sources is rebuilt by every load like the other bulk tables: one
-- thumbnail URL per package, straight from UDD. package_screenshots holds the
-- mirrored bytes and outlives loads; ingest refreshes it after every run and
-- prunes what fell out of the top set.

CREATE TABLE screenshot_sources (
    package text PRIMARY KEY,
    small_image_url text NOT NULL
);

CREATE TABLE package_screenshots (
    package text PRIMARY KEY,
    source_url text NOT NULL,
    content_type text NOT NULL,
    image bytea NOT NULL,
    fetched_at timestamptz NOT NULL
);

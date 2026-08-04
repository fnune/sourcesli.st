-- Current point-release versions on cdimage, observed by ingest. The image
-- chooser builds exact download filenames from these rows; compiled-in
-- fallbacks in internal/images cover an empty table. Not replaced by the
-- loader: the rows outlive any single sync.

CREATE TABLE iso_versions (
    channel text PRIMARY KEY,
    version text NOT NULL,
    observed_at timestamptz NOT NULL
);

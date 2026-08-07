-- What cdimage serves each image at, observed by ingest.
--
-- The download page lists twelve images side by side, so a size on a tile reads
-- as a comparison with the tiles beside it. Rounded guesses in the code cannot
-- carry that: every live image is not the same size, and the figure changes with
-- every point release. Keyed by the catalogue's own identifier, because the
-- catalogue is what decides which images exist.

CREATE TABLE iso_sizes (
    image text PRIMARY KEY,
    bytes bigint NOT NULL,
    observed_at timestamptz NOT NULL
);

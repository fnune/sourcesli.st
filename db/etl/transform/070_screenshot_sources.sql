-- Transform: udd_raw.screenshots -> screenshot_sources__new
--
-- One thumbnail URL per package. screenshots.debian.net hosts several shots
-- for some packages; the first by URL is a deterministic pick, and which one
-- wins matters little for a thumbnail. Ingest downloads from these URLs after
-- the load; the web tier never touches them.

INSERT INTO screenshot_sources__new (package, small_image_url)
SELECT DISTINCT ON (package)
    package,
    small_image_url
FROM udd_raw.screenshots
WHERE small_image_url <> ''
ORDER BY package ASC, screenshot_url ASC
ON CONFLICT (package) DO NOTHING;

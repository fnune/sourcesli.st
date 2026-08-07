-- Queries the web tier runs. Every one is bounded and served by an index
-- created in db/migrations.

-- name: SearchPackages :many
-- Full-text search across package names and synopses, most popular first.
--
-- Full-text search matches whole words, so `fire` finds "Fire an event" and not
-- firefox-esr: to Postgres those are different lexemes. A reader typing the
-- start of a name means the name, and the autocompletion beside this query
-- already behaves that way, so a prefix of a name matches here too and sorts
-- above the prose. Both halves are served by their own index.
--
-- A name can appear as both a binary and a source package. Collapsing to one
-- row keeps a reader from seeing the same name twice with no way to tell why;
-- the binary wins because that is the page a search result links to.
SELECT
    name,
    kind,
    suite,
    version,
    synopsis,
    popularity
FROM (
    SELECT DISTINCT ON (name)
        name,
        kind,
        suite,
        version,
        synopsis,
        popularity,
        name LIKE lower(sqlc.arg(query)::text) || '%' AS names_it
    FROM package_search
    WHERE
        suite = sqlc.arg(suite)
        AND (
            document @@ websearch_to_tsquery('english', sqlc.arg(query))
            OR name LIKE lower(sqlc.arg(query)::text) || '%'
        )
    ORDER BY name ASC, (kind = 'binary') DESC
) AS deduplicated
ORDER BY names_it DESC, popularity DESC, name ASC
LIMIT sqlc.arg(max_results);

-- name: CompletePackageNames :many
-- Autocompletion. Prefix matches come first because that is what a reader
-- typing a name expects; trigram similarity fills what remains so a typo still
-- finds something. Each half is served by its own index and the LIMIT is
-- small: this runs on every keystroke.
SELECT
    name,
    suite,
    version,
    synopsis,
    popularity
FROM package_search
WHERE
    suite = sqlc.arg(suite)
    AND kind = 'binary'
    AND (
        name LIKE sqlc.arg(prefix)::text || '%'
        OR name % sqlc.arg(prefix)::text
    )
ORDER BY
    (name LIKE sqlc.arg(prefix)::text || '%') DESC,
    popularity DESC,
    name ASC
LIMIT sqlc.arg(max_results);

-- name: SuggestSimilarPackages :many
-- What to offer a reader whose search matched nothing. Full-text search matches
-- whole words, so a misspelt name matches nothing at all; edit distance finds
-- what they meant. Trigram similarity does not: a typo in the middle of a short
-- name shares too few trigrams with it.
--
-- The distance is measured against the name and against its leading word, so
-- `faiafox` reaches `firefox-esr`, and the allowance grows with the length of
-- the query because two wrong letters in four is not a typo. Names are collapsed
-- to one per leading word, or a package with thirty translation packages fills
-- the list on its own.
--
-- This is the one query here no index serves, and it stays acceptable because
-- it runs only when a search returned nothing at all.
WITH input AS (
    SELECT
        sqlc.arg(query)::text AS query,
        least(3, greatest(1, ceil(length(sqlc.arg(query)::text) / 3.0)))::int
            AS max_distance
),

candidates AS (
    SELECT DISTINCT ON (split_part(package_search.name, '-', 1))
        package_search.name,
        package_search.suite,
        package_search.synopsis,
        package_search.popularity,
        least(
            levenshtein_less_equal(package_search.name, input.query, input.max_distance),
            levenshtein_less_equal(
                split_part(package_search.name, '-', 1), input.query, input.max_distance
            )
        ) AS distance
    FROM package_search, input
    WHERE
        package_search.suite = sqlc.arg(suite)
        AND package_search.kind = 'binary'
        AND least(
            levenshtein_less_equal(package_search.name, input.query, input.max_distance),
            levenshtein_less_equal(
                split_part(package_search.name, '-', 1), input.query, input.max_distance
            )
        ) <= input.max_distance
    -- Within a family the plainest name is the one the reader meant: `gimp`
    -- rather than `gimp-data`, which popcon counts just as often.
    ORDER BY
        split_part(package_search.name, '-', 1),
        distance ASC,
        length(package_search.name) ASC,
        popularity DESC
)

SELECT
    name,
    suite,
    synopsis
FROM candidates
ORDER BY distance ASC, popularity DESC, name ASC
LIMIT sqlc.arg(max_results);

-- name: GetBinaryPackage :many
-- Every architecture and version of one binary package in one suite.
SELECT
    name,
    version,
    architecture,
    suite,
    source_name,
    source_version,
    section,
    priority,
    essential,
    installed_size,
    size,
    homepage,
    synopsis,
    description,
    depends,
    pre_depends,
    recommends,
    suggests,
    enhances,
    breaks,
    conflicts,
    provides,
    replaces
FROM binary_packages
WHERE name = $1 AND suite = $2
ORDER BY architecture ASC;

-- name: GetSourcePackage :one
SELECT
    name,
    version,
    suite,
    component,
    maintainer_name,
    maintainer_email,
    uploaders,
    section,
    priority,
    homepage,
    vcs_type,
    vcs_url,
    vcs_browser,
    standards_version
FROM source_packages
WHERE name = $1 AND suite = $2
ORDER BY version DESC
LIMIT 1;

-- name: ListSuites :many
SELECT
    name,
    codename,
    version,
    role,
    sort_order
FROM suites
ORDER BY sort_order ASC;

-- name: GetFreshVersion :one
-- The freshness overlay, read in preference to binary_packages. Absent rows are
-- the normal case: the overlay only holds what changed since the last bulk load.
SELECT
    name,
    architecture,
    suite,
    version,
    observed_at
FROM fresh_binary_versions
WHERE name = $1 AND suite = $2 AND architecture = $3;

-- name: GetLastSync :one
-- Drives the "data as of" line every page carries.
SELECT
    synced_at,
    etag
FROM upstream_state
WHERE id;

-- name: ListPackageSuites :many
-- Which suites carry a package and at what version, for the suite switcher on
-- the package page. A binary row wins over a source row of the same name; the
-- version is the search table's approximation, good enough for a tab label.
SELECT DISTINCT ON (suite)
    suite,
    version
FROM package_search
WHERE name = $1
ORDER BY suite ASC, (kind = 'binary') DESC;

-- name: ListTopPackages :many
-- The most used applications in one suite, for the front page. The transform
-- decides what counts as an application; votes is popcon's count of systems
-- that actually use the package, which ranks firefox above plumbing that is
-- merely installed everywhere.
SELECT
    name,
    suite,
    synopsis,
    votes,
    exists(
        SELECT 1 FROM package_screenshots AS x
        WHERE x.package = package_search.name
    ) AS has_screenshot
FROM package_search
WHERE suite = sqlc.arg(suite) AND kind = 'binary' AND application
ORDER BY votes DESC, name ASC
LIMIT sqlc.arg(max_results);

-- name: GetScreenshot :one
-- A mirrored thumbnail. Served by us so a visitor request never reaches
-- screenshots.debian.net.
SELECT
    content_type,
    image
FROM package_screenshots
WHERE package = $1;

-- name: ListISOVersions :many
-- The point releases ingest last observed on cdimage. The image chooser
-- builds exact download filenames from them.
SELECT
    channel,
    version
FROM iso_versions;

-- name: ListPackageVersions :many
-- The versions of a handful of named packages in one suite. The download page
-- states what a live image's desktop ships as, and this is where that number
-- comes from: our own copy of the archive rather than a constant in the code.
SELECT
    name,
    version
FROM package_search
WHERE
    suite = sqlc.arg(suite)
    AND kind = 'binary'
    AND name = any(sqlc.arg(names)::text []);

-- name: ListISOSizes :many
-- The sizes ingest last observed on cdimage, keyed by the catalogue identifier
-- the download page addresses each image by.
SELECT
    image,
    bytes
FROM iso_sizes;

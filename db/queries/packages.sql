-- Queries the web tier runs. Every one is bounded and served by an index
-- created in db/migrations.

-- name: SearchPackages :many
-- Full-text search across package names and synopses, most popular first.
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
        popularity
    FROM package_search
    WHERE
        suite = sqlc.arg(suite)
        AND document @@ websearch_to_tsquery('english', sqlc.arg(query))
    ORDER BY name ASC, (kind = 'binary') DESC
) AS deduplicated
ORDER BY popularity DESC, name ASC
LIMIT sqlc.arg(max_results);

-- name: CompletePackageNames :many
-- Autocompletion. Prefix matching first because that is what a reader typing a
-- name expects, with trigram similarity as the tiebreak so a typo still finds
-- something. LIMIT is small: this runs on every keystroke.
SELECT
    name,
    suite,
    synopsis,
    popularity
FROM package_search
WHERE
    suite = sqlc.arg(suite)
    AND kind = 'binary'
    AND name LIKE sqlc.arg(prefix)::text || '%'
ORDER BY popularity DESC, name ASC
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

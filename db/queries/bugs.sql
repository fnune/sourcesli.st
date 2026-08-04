-- name: ListBugsForPackage :many
-- Bugs against a binary package or its source, most severe first.
--
-- Archived bugs are excluded because a package page is about the present. The
-- partial indexes in db/migrations cover exactly this predicate. Status and
-- severity are optional filters where the empty string means "any"; status is
-- otherwise 'open' or 'closed'.
SELECT
    number,
    package,
    source,
    title,
    severity,
    status,
    submitter_name,
    arrival,
    last_modified,
    done,
    affects_stable,
    affects_testing,
    affects_unstable
FROM bugs
WHERE
    NOT archived
    AND (package = sqlc.arg(name) OR source = sqlc.arg(name))
    AND (sqlc.arg(status)::text = '' OR done = (sqlc.arg(status)::text = 'closed'))
    AND (sqlc.arg(severity)::text = '' OR severity = sqlc.arg(severity)::text)
ORDER BY
    CASE severity
        WHEN 'critical' THEN 0
        WHEN 'grave' THEN 1
        WHEN 'serious' THEN 2
        WHEN 'important' THEN 3
        WHEN 'normal' THEN 4
        WHEN 'minor' THEN 5
        WHEN 'wishlist' THEN 6
        ELSE 7
    END ASC,
    number DESC
LIMIT sqlc.arg(max_results);

-- name: GetBug :one
SELECT
    number,
    package,
    source,
    title,
    severity,
    status,
    submitter_name,
    owner,
    forwarded,
    arrival,
    last_modified,
    done,
    archived,
    affects_stable,
    affects_testing,
    affects_unstable,
    affects_experimental
FROM bugs
WHERE number = $1;

-- name: ListBugTags :many
SELECT tag
FROM bug_tags
WHERE bug_number = $1
ORDER BY tag ASC;

-- name: CountOpenBugsForPackage :one
SELECT count(*) AS total
FROM bugs
WHERE NOT archived AND NOT done AND (package = $1 OR source = $1);

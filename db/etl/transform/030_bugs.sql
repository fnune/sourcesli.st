-- Transform: udd_raw.bugs and udd_raw.archived_bugs -> bugs__new
--
-- UDD's `done` column holds the address of whoever closed the bug, not a flag.
-- The flag we want is derived from `status`, which is what the BTS actually
-- treats as authoritative.
--
-- Archived bugs live in a separate upstream table with the same shape. They are
-- unioned in with a marker rather than kept apart, because a reader looking up a
-- bug number does not know in advance which side it is on.

INSERT INTO bugs__new (
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
)
SELECT
    b.id,
    coalesce(b.package, ''),
    coalesce(b.source, ''),
    coalesce(b.title, ''),
    coalesce(b.severity, ''),
    coalesce(b.status, ''),
    coalesce(b.submitter_name, ''),
    coalesce(b.owner, ''),
    coalesce(b.forwarded, ''),
    -- UDD stores these as `timestamp without time zone` holding UTC. Assigning
    -- one to our timestamptz column without saying so would interpret it in
    -- whatever the session timezone happens to be, silently shifting every bug
    -- date by the server's offset.
    b.arrival AT TIME ZONE 'UTC',
    b.last_modified AT TIME ZONE 'UTC',
    coalesce(b.status, '') = 'done',
    b.archived,
    coalesce(b.affects_stable, false),
    coalesce(b.affects_testing, false),
    coalesce(b.affects_unstable, false),
    coalesce(b.affects_experimental, false)
FROM (
    SELECT
        id,
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
        affects_stable,
        affects_testing,
        affects_unstable,
        affects_experimental,
        false AS archived
    FROM udd_raw.bugs
    UNION ALL
    SELECT
        id,
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
        affects_stable,
        affects_testing,
        affects_unstable,
        affects_experimental,
        true AS archived
    FROM udd_raw.archived_bugs
) AS b
ON CONFLICT (number) DO NOTHING;

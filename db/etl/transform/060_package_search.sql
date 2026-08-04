-- Transform: the staged tables -> package_search__new
--
-- Runs last, reading the other __new tables rather than udd_raw, so search
-- always describes exactly the data the swap is about to publish.
--
-- One row per name per suite, collapsing architectures and versions. A prefix
-- query for autocompletion must never fan out across every architecture of
-- every version of a package, which is what makes this table worth its cost.
--
-- Known limitation: max(version) sorts lexically, so it picks 1.9 over 1.10.
-- Postgres has no dpkg version comparison and a correct implementation is a
-- sizeable PL/pgSQL function, so this stays approximate until search results
-- are shown to need better. The package page reads binary_packages directly and
-- is authoritative; only the version rendered next to a search hit can be wrong.

INSERT INTO package_search__new (
    name, kind, suite, version, synopsis, popularity, document
)
SELECT
    b.name,
    'binary',
    b.suite,
    max(b.version),
    coalesce(max(b.synopsis), ''),
    coalesce(max(p.installed), 0),
    to_tsvector('english', b.name || ' ' || coalesce(max(b.synopsis), ''))
FROM binary_packages__new AS b
LEFT JOIN popcon__new AS p ON p.package = b.name
GROUP BY b.name, b.suite
ON CONFLICT (name, kind, suite) DO NOTHING;

-- Popcon counts installations of binary packages, so a source package's
-- popularity is that of the most installed binary built from it. Joining the
-- source name against popcon directly would leave every source whose binaries
-- carry other names at zero.
INSERT INTO package_search__new (
    name, kind, suite, version, synopsis, popularity, document
)
SELECT
    s.name,
    'source',
    s.suite,
    max(s.version),
    '',
    coalesce(max(bp.installed), 0),
    to_tsvector('english', s.name)
FROM source_packages__new AS s
LEFT JOIN (
    SELECT
        b.source_name,
        b.suite,
        max(p.installed) AS installed
    FROM binary_packages__new AS b
    INNER JOIN popcon__new AS p ON p.package = b.name
    GROUP BY b.source_name, b.suite
) AS bp ON bp.source_name = s.name AND bp.suite = s.suite
GROUP BY s.name, s.suite
ON CONFLICT (name, kind, suite) DO NOTHING;

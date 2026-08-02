-- Transform: udd_raw.sources -> source_packages__new
--
-- This directory is the only place in the repository that names UDD columns.
-- Everything downstream reads our schema, so an upstream rename is contained
-- here.
--
-- Every column these queries reference was checked against the real dump on
-- 2026-08-02 with `pg_restore --schema-only`. Re-run that check rather than
-- guessing when a transform starts failing after an upstream change.
--
-- UDD carries Ubuntu alongside Debian, hence the distribution filter. It also
-- refers to a suite sometimes by name and sometimes by codename, so the join
-- accepts either; suites.name and suites.codename never collide.

INSERT INTO source_packages__new (
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
)
SELECT
    s.source,
    s.version,
    suites.name,
    coalesce(s.component, ''),
    coalesce(s.maintainer_name, ''),
    coalesce(s.maintainer_email, ''),
    coalesce(s.uploaders, ''),
    coalesce(s.section, ''),
    coalesce(s.priority, ''),
    coalesce(s.homepage, ''),
    coalesce(s.vcs_type, ''),
    coalesce(s.vcs_url, ''),
    coalesce(s.vcs_browser, ''),
    coalesce(s.standards_version, '')
FROM udd_raw.sources AS s
INNER JOIN suites ON s.release IN (suites.name, suites.codename)
WHERE s.distribution = 'debian'
ON CONFLICT (name, version, suite) DO NOTHING;

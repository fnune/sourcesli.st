-- Transform: udd_raw.packages -> binary_packages__new
--
-- UDD stores the whole Description field in one column. Debian's convention is
-- that the first line is the synopsis and the remainder is the extended
-- description, so they are split here rather than in the web tier: the split is
-- a property of the source format, not of how a page happens to render.

INSERT INTO binary_packages__new (
    name,
    version,
    architecture,
    suite,
    component,
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
)
SELECT
    p.package,
    p.version,
    p.architecture,
    suites.name,
    coalesce(p.component, ''),
    coalesce(p.source, p.package),
    coalesce(p.source_version, p.version),
    coalesce(p.section, ''),
    coalesce(p.priority, ''),
    coalesce(p.essential, '') = 'yes',
    p.installed_size,
    p.size,
    coalesce(p.homepage, ''),
    split_part(coalesce(p.description, ''), E'\n', 1),
    coalesce(p.description, ''),
    coalesce(p.depends, ''),
    coalesce(p.pre_depends, ''),
    coalesce(p.recommends, ''),
    coalesce(p.suggests, ''),
    coalesce(p.enhances, ''),
    coalesce(p.breaks, ''),
    coalesce(p.conflicts, ''),
    coalesce(p.provides, ''),
    coalesce(p.replaces, '')
FROM udd_raw.packages AS p
INNER JOIN suites ON p.release IN (suites.name, suites.codename)
WHERE p.distribution = 'debian'
ON CONFLICT (name, version, architecture, suite) DO NOTHING;

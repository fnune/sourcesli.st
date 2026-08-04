-- Scratch schema for the extract step.
--
-- UDD's own tables use types we would otherwise have to install to read them:
-- `debversion` comes from a Debian-specific Postgres extension, and
-- `bugs_severity` is an enum defined in UDD's schema. A selective
-- `pg_restore --table` carries neither, so restoring UDD's DDL fails outright.
--
-- So we declare the tables ourselves with permissive types and restore
-- `--data-only` into them. The column names and order match UDD exactly, which
-- is what the COPY stream needs; the types are deliberately wider, because this
-- schema exists to receive bytes rather than to enforce anything. The transform
-- queries do the narrowing.
--
-- Generated from the real dump on 2026-08-02 and checked in rather than derived
-- at runtime, so a change in UDD's shape shows up as a diff in review.

CREATE TABLE public.packages (
    package text NOT NULL,
    version text NOT NULL,
    architecture text NOT NULL,
    maintainer text,
    maintainer_name text,
    maintainer_email text,
    description text,
    description_md5 text,
    source text,
    source_version text,
    essential text,
    depends text,
    recommends text,
    suggests text,
    enhances text,
    pre_depends text,
    breaks text,
    installed_size bigint,
    homepage text,
    size bigint,
    build_essential text,
    origin text,
    sha1 text,
    replaces text,
    section text,
    md5sum text,
    bugs text,
    priority text,
    tag text,
    task text,
    python_version text,
    ruby_versions text,
    provides text,
    conflicts text,
    sha256 text,
    original_maintainer text,
    distribution text NOT NULL,
    release text NOT NULL,
    component text NOT NULL,
    multi_arch text,
    package_type text,
    filename text
);

CREATE TABLE public.sources (
    source text NOT NULL,
    version text NOT NULL,
    maintainer text,
    maintainer_name text,
    maintainer_email text,
    format text,
    files text,
    uploaders text,
    bin text,
    architecture text,
    standards_version text,
    homepage text,
    build_depends text,
    build_depends_indep text,
    build_conflicts text,
    build_conflicts_indep text,
    priority text,
    section text,
    distribution text NOT NULL,
    release text NOT NULL,
    component text NOT NULL,
    vcs_type text,
    vcs_url text,
    vcs_browser text,
    python_version text,
    ruby_versions text,
    checksums_sha1 text,
    checksums_sha256 text,
    original_maintainer text,
    dm_upload_allowed boolean,
    testsuite text,
    autobuild text,
    extra_source_only boolean,
    build_depends_arch text,
    build_conflicts_arch text,
    testsuite_triggers text,
    dgit text,
    git_tag_info text,
    git_tag_tagger text
);

CREATE TABLE public.bugs (
    id integer NOT NULL,
    package text,
    source text,
    arrival timestamp without time zone,
    status text,
    severity text,
    submitter text,
    submitter_name text,
    submitter_email text,
    owner text,
    owner_name text,
    owner_email text,
    done text,
    done_name text,
    done_email text,
    title text,
    last_modified timestamp without time zone,
    forwarded text,
    affects_oldstable boolean,
    affects_stable boolean,
    affects_testing boolean,
    affects_unstable boolean,
    affects_experimental boolean,
    affected_packages text,
    affected_sources text
);

CREATE TABLE public.bugs_tags (
    id integer NOT NULL,
    tag text NOT NULL
);

CREATE TABLE public.archived_bugs (
    id integer NOT NULL,
    package text,
    source text,
    arrival timestamp without time zone,
    status text,
    severity text,
    submitter text,
    submitter_name text,
    submitter_email text,
    owner text,
    owner_name text,
    owner_email text,
    done text,
    done_name text,
    done_email text,
    title text,
    last_modified timestamp without time zone,
    forwarded text,
    affects_oldstable boolean,
    affects_stable boolean,
    affects_testing boolean,
    affects_unstable boolean,
    affects_experimental boolean,
    affected_packages text,
    affected_sources text
);

CREATE TABLE public.popcon (
    package text NOT NULL,
    insts integer,
    vote integer,
    olde integer,
    recent integer,
    nofiles integer
);

CREATE TABLE public.debtags (
    package text,
    tag text
);

CREATE TABLE public.screenshots (
    package text NOT NULL,
    version text,
    homepage text,
    maintainer_name text,
    maintainer_email text,
    description text,
    section text,
    screenshot_url text NOT NULL,
    large_image_url text NOT NULL,
    small_image_url text NOT NULL
);

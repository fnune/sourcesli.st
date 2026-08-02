-- Our own schema. UDD's shape is an input to the transform step, never a
-- contract: nothing below mirrors an upstream table one to one.
--
-- Bulk tables carry no foreign keys between each other. The loader replaces
-- them by building `<table>__new` and renaming, and `CREATE TABLE ... LIKE`
-- does not carry foreign keys across that swap. Referential integrity is
-- established by the transform queries instead, which is the usual trade for a
-- read-only replica rebuilt from a single upstream snapshot.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE suites (
    name text PRIMARY KEY,
    codename text NOT NULL DEFAULT '',
    version text NOT NULL DEFAULT '',
    role text NOT NULL,
    sort_order integer NOT NULL
);

CREATE TABLE source_packages (
    name text NOT NULL,
    version text NOT NULL,
    suite text NOT NULL,
    component text NOT NULL DEFAULT '',
    maintainer_name text NOT NULL DEFAULT '',
    maintainer_email text NOT NULL DEFAULT '',
    uploaders text NOT NULL DEFAULT '',
    section text NOT NULL DEFAULT '',
    priority text NOT NULL DEFAULT '',
    homepage text NOT NULL DEFAULT '',
    vcs_type text NOT NULL DEFAULT '',
    vcs_url text NOT NULL DEFAULT '',
    vcs_browser text NOT NULL DEFAULT '',
    standards_version text NOT NULL DEFAULT '',
    PRIMARY KEY (name, version, suite)
);

CREATE INDEX source_packages_name_idx ON source_packages (name);
CREATE INDEX source_packages_maintainer_email_idx ON source_packages (
    maintainer_email
);

CREATE TABLE binary_packages (
    name text NOT NULL,
    version text NOT NULL,
    architecture text NOT NULL,
    suite text NOT NULL,
    component text NOT NULL DEFAULT '',
    source_name text NOT NULL DEFAULT '',
    source_version text NOT NULL DEFAULT '',
    section text NOT NULL DEFAULT '',
    priority text NOT NULL DEFAULT '',
    essential boolean NOT NULL DEFAULT false,
    installed_size bigint,
    size bigint,
    homepage text NOT NULL DEFAULT '',
    synopsis text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    depends text NOT NULL DEFAULT '',
    pre_depends text NOT NULL DEFAULT '',
    recommends text NOT NULL DEFAULT '',
    suggests text NOT NULL DEFAULT '',
    enhances text NOT NULL DEFAULT '',
    breaks text NOT NULL DEFAULT '',
    conflicts text NOT NULL DEFAULT '',
    provides text NOT NULL DEFAULT '',
    replaces text NOT NULL DEFAULT '',
    PRIMARY KEY (name, version, architecture, suite)
);

CREATE INDEX binary_packages_name_idx ON binary_packages (name);
CREATE INDEX binary_packages_source_idx ON binary_packages (source_name, suite);

CREATE TABLE bugs (
    number integer PRIMARY KEY,
    package text NOT NULL DEFAULT '',
    source text NOT NULL DEFAULT '',
    title text NOT NULL DEFAULT '',
    severity text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    submitter_name text NOT NULL DEFAULT '',
    owner text NOT NULL DEFAULT '',
    forwarded text NOT NULL DEFAULT '',
    arrival timestamptz,
    last_modified timestamptz,
    done boolean NOT NULL DEFAULT false,
    archived boolean NOT NULL DEFAULT false,
    affects_stable boolean NOT NULL DEFAULT false,
    affects_testing boolean NOT NULL DEFAULT false,
    affects_unstable boolean NOT NULL DEFAULT false,
    affects_experimental boolean NOT NULL DEFAULT false
);

CREATE INDEX bugs_package_idx ON bugs (package) WHERE NOT archived;
CREATE INDEX bugs_source_idx ON bugs (source) WHERE NOT archived;
CREATE INDEX bugs_severity_idx ON bugs (severity);

CREATE TABLE bug_tags (
    bug_number integer NOT NULL,
    tag text NOT NULL,
    PRIMARY KEY (bug_number, tag)
);

CREATE INDEX bug_tags_tag_idx ON bug_tags (tag);

CREATE TABLE popcon (
    package text PRIMARY KEY,
    installed integer NOT NULL DEFAULT 0,
    votes integer NOT NULL DEFAULT 0,
    recent integer NOT NULL DEFAULT 0
);

-- Denormalised surface for search and autocompletion. One row per package name
-- per suite, so a prefix query never touches the version-level tables.
CREATE TABLE package_search (
    name text NOT NULL,
    kind text NOT NULL,
    suite text NOT NULL,
    version text NOT NULL,
    synopsis text NOT NULL DEFAULT '',
    popularity integer NOT NULL DEFAULT 0,
    document tsvector NOT NULL,
    PRIMARY KEY (name, kind, suite)
);

CREATE INDEX package_search_name_trgm_idx ON package_search USING gin (
    name gin_trgm_ops
);
CREATE INDEX package_search_name_prefix_idx ON package_search (
    name text_pattern_ops
);
CREATE INDEX package_search_document_idx ON package_search USING gin (document);
CREATE INDEX package_search_popularity_idx ON package_search (popularity DESC);

-- Hourly delta overlay, written by the tier 2 sync from deb.debian.org and
-- truncated by every tier 1 load. The web tier reads it with priority over
-- binary_packages and falls back when it is empty, so a failed delta sync
-- degrades to dump freshness instead of breaking a page.
--
-- Kept separate from binary_packages because the two tiers write incompatibly:
-- tier 1 replaces whole tables by renaming them into place, and an incremental
-- writer cannot participate in that swap.
CREATE TABLE fresh_binary_versions (
    name text NOT NULL,
    architecture text NOT NULL,
    suite text NOT NULL,
    version text NOT NULL,
    source_name text NOT NULL DEFAULT '',
    synopsis text NOT NULL DEFAULT '',
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (name, architecture, suite)
);

CREATE INDEX fresh_binary_versions_name_idx ON fresh_binary_versions (name);

-- Not replaced by the loader: these outlive any single sync.

CREATE TABLE upstream_state (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    etag text,
    last_modified text,
    synced_at timestamptz
);

CREATE TABLE sync_runs (
    id bigserial PRIMARY KEY,
    source text NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    outcome text NOT NULL,
    etag text,
    last_modified text,
    rows_written bigint NOT NULL DEFAULT 0,
    error text
);

CREATE INDEX sync_runs_started_at_idx ON sync_runs (started_at DESC);

-- Transform: the staged tables -> package_search__new
--
-- Runs last, reading the other __new tables so search always describes
-- exactly the data the swap is about to publish. The only udd_raw reads are
-- debtags and screenshots, which have no staged equivalent: they feed one
-- boolean and nothing else.
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

-- `application` marks packages a person plausibly chose to install and run,
-- so the front page can rank them by use. No single source is reliable alone:
-- debtags is curated but stale (firefox-esr carries no tags), screenshots is
-- broad but noisy (libraries have joke screenshots), and dependency sniffing
-- catches GUI programs but also desktop plumbing. A package qualifies when it
-- has a screenshot plus a GUI toolkit dependency, or carries the
-- graphical-program debtags, minus libraries, transitional packages and a
-- denylist of desktop-infrastructure name patterns. Tune the denylist when the
-- front page surfaces plumbing again.
INSERT INTO package_search__new (
    name, kind, suite, version, synopsis, popularity, votes, application, document
)
SELECT
    b.name,
    'binary',
    b.suite,
    max(b.version),
    coalesce(max(b.synopsis), ''),
    coalesce(max(p.installed), 0),
    coalesce(max(p.votes), 0),
    (
        b.name NOT LIKE 'lib%'
        AND b.name !~ (
            '^(gnome-(shell|session|menus|user-share|initial-setup'
            || '|control-center|keyring|remote-desktop)'
            || '|gdm|sddm|lightdm|ibus|x11-|xdg-|kde-config-|plasma-|breeze'
            || '|powerdevil|drkonqi|systemsettings|zenity|exo-'
            || '|nm-connection-editor|bluedevil|kup-backup|print-manager'
            || '|xfce4-(notifyd|session|settings|panel|power)'
            || '|xfwm|xfdesktop|mutter|kwin|baloo|kactivit|polkit-|speech-'
            || '|kio-|qml6?-module|gir1|mate-polkit|kinfocenter)'
            || '|-(plugins?|volman|runtime)$'
        )
        -- A package kept only so an upgrade pulls its replacement in. It has
        -- the screenshots and the dependencies of the program it used to be.
        AND bool_and(b.synopsis !~* 'transitional')
        AND (
            (
                EXISTS (
                    SELECT 1 FROM udd_raw.screenshots AS s
                    WHERE s.package = b.name
                )
                AND bool_or(
                    b.priority = 'optional'
                    AND NOT b.essential
                    AND b.depends ~ (
                        'libgtk-[34]|libgtk-x11|libqt[56]gui|libqt[56]widgets'
                        || '|libadwaita|libsdl[12]|libwx|libfltk|electron'
                    )
                )
            )
            OR (
                EXISTS (
                    SELECT 1 FROM udd_raw.debtags AS gx
                    WHERE gx.package = b.name AND gx.tag = 'interface::x11'
                )
                AND EXISTS (
                    SELECT 1 FROM udd_raw.debtags AS rp
                    WHERE rp.package = b.name AND rp.tag = 'role::program'
                )
                AND bool_or(b.priority = 'optional' AND NOT b.essential)
            )
        )
    ),
    to_tsvector('english', b.name || ' ' || coalesce(max(b.synopsis), ''))
FROM binary_packages__new AS b
LEFT JOIN popcon__new AS p ON p.package = b.name
GROUP BY b.name, b.suite
ON CONFLICT (name, kind, suite) DO NOTHING;

-- A desktop environment's built-in applications inherit the environment's whole
-- install base, so ranking by votes would list what ships with Xfce rather than
-- what people chose. Finding them means walking the dependency graph out from
-- each desktop, which is what the next three statements do.
--
-- Only unconditional relationships are edges. An alternative (`firefox-esr |
-- chromium | ...`) is the packaging telling us a choice was made, and the
-- choice is exactly what the front page is trying to show; following it would
-- drop firefox-esr, which gnome-core recommends that way. Recommends are edges
-- because apt installs them by default, and Debian puts most of a desktop's
-- applications there rather than in Depends.
CREATE TEMP TABLE desktop_edges AS
SELECT DISTINCT
    b.suite,
    b.name AS parent,
    -- `libfoo (>= 1.2) [amd64] <!nocheck>` -> `libfoo`
    trim(regexp_replace(split_part(relation, '[', 1), '[(:<].*$', '')) AS child,
    field.required
FROM
    binary_packages__new AS b,
    LATERAL (VALUES (b.depends, true), (b.recommends, false))
        AS field (list, required),
    unnest(string_to_array(field.list, ',')) AS relation
WHERE relation NOT LIKE '%|%' AND trim(relation) <> '';

CREATE INDEX ON desktop_edges (suite, parent);
ANALYZE desktop_edges;

-- Walking out from the tasksel desktop tasks, because those are what a Debian
-- installation actually offers to install: whatever they reach is what a person
-- gets without asking for it by name.
--
-- The tasks themselves are curated bundles, not desktops. task-kde-desktop
-- recommends gimp, which nobody would call part of KDE, so a package the task
-- merely recommends is only a starting point when its own section says it
-- belongs to a desktop. Anything the task depends on is the desktop by
-- definition.
--
-- The walk stops at applications. Once it reaches a program a person runs, that
-- program's dependencies are its own business: ark pulls in unar, gimp pulls in
-- graphviz, and neither says anything about the desktop that recommended them.
CREATE TEMP TABLE desktop_builtins AS
WITH RECURSIVE roots AS (
    SELECT DISTINCT
        e.suite,
        e.child AS name
    FROM binary_packages__new AS t
    INNER JOIN desktop_edges AS e ON e.parent = t.name AND e.suite = t.suite
    WHERE
        t.section = 'tasks'
        AND t.name ~ '(^|-)desktop$'
        AND (
            e.required
            OR EXISTS (
                SELECT 1
                FROM binary_packages__new AS b
                WHERE
                    b.name = e.child
                    AND b.suite = e.suite
                    AND b.section IN ('gnome', 'kde', 'xfce', 'x11')
            )
        )
),

-- The depth bound is a cycle guard, not a tuning knob: Debian has dependency
-- loops and the walk runs dry well before ten hops.
reachable (suite, name, depth) AS (
    SELECT
        suite,
        name,
        0
    FROM roots
    UNION
    SELECT
        e.suite,
        e.child,
        r.depth + 1
    FROM reachable AS r
    INNER JOIN desktop_edges AS e ON e.parent = r.name AND e.suite = r.suite
    WHERE
        r.depth < 10
        AND NOT EXISTS (
            SELECT 1
            FROM package_search__new AS ps
            WHERE
                ps.name = r.name
                AND ps.suite = r.suite
                AND ps.kind = 'binary'
                AND ps.application
        )
)

SELECT DISTINCT
    suite,
    name
FROM reachable;

CREATE INDEX ON desktop_builtins (suite, name);
ANALYZE desktop_builtins;

UPDATE package_search__new AS ps
SET application = false
FROM desktop_builtins AS d
WHERE
    d.suite = ps.suite
    AND d.name = ps.name
    AND ps.kind = 'binary'
    AND ps.application;

DROP TABLE desktop_edges, desktop_builtins;

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

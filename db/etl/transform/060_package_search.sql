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
-- graphical-program debtags, minus libraries and a denylist of
-- desktop-infrastructure name patterns. Tune the denylist when the front page
-- surfaces plumbing again.
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
            || '|-(plugins?|volman)$'
        )
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

-- A desktop environment's default applications inherit the environment's whole
-- install base, so ranking by votes would list what ships with GNOME rather
-- than what people chose. A package pulled in by a popular metapackage
-- (gnome-core, kde-standard) is unflagged; the threshold spares niche
-- metapackages, which is what keeps firefox-esr in despite reform-desktop-full
-- depending on it. Runs as a second pass so the expensive dependency match
-- only touches the few thousand flagged rows.
UPDATE package_search__new AS ps
SET application = false
WHERE
    ps.kind = 'binary'
    AND ps.application
    AND EXISTS (
        SELECT 1
        FROM binary_packages__new AS m
        INNER JOIN popcon__new AS pm ON pm.package = m.name
        WHERE
            m.suite = ps.suite
            AND m.section = 'metapackages'
            AND pm.installed > 500
            AND m.depends ~ (
                '\m'
                || regexp_replace(ps.name, '([^[:alnum:]])', '\\\1', 'g')
                || '\M'
            )
    );

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

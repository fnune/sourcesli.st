-- Suite metadata is ours, not UDD's.
--
-- Debian's suite set changes roughly every two years and carries editorial
-- decisions the ISO chooser depends on (which suite a newcomer should be
-- offered, what order to present them in). Deriving it from an upstream table
-- would hand those decisions to a schema we do not control, to save editing
-- six rows per release cycle.
--
-- The transform step fails when a release appears in UDD with no row here, so
-- a new codename cannot silently produce packages that belong to no suite.

INSERT INTO suites (name, codename, version, role, sort_order) VALUES
('oldstable', 'bookworm', '12', 'oldstable', 10),
('stable', 'trixie', '13', 'stable', 20),
('testing', 'forky', '', 'testing', 30),
('unstable', 'sid', '', 'unstable', 40),
('experimental', 'rc-buggy', '', 'experimental', 50)
ON CONFLICT (name) DO UPDATE SET
    codename = excluded.codename,
    version = excluded.version,
    role = excluded.role,
    sort_order = excluded.sort_order;

package etl_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"testing/fstest"

	sourceslist "github.com/fnune/sourcesli.st"
	"github.com/fnune/sourcesli.st/internal/etl"
	"github.com/fnune/sourcesli.st/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These run against a real Postgres because the parts most likely to be wrong
// are the parts a fake cannot have an opinion about: whether the transform SQL
// is valid, whether the index rebuild produces a usable table, and whether the
// rename swap actually publishes.
//
// Skipped rather than failed when there is no database, so `nix flake check`
// stays runnable in a sandbox with no network. CI sets the variable.
func requireDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the integration tests")
	}

	pool, err := store.Open(t.Context(), databaseURL, "sourceslist-tests")
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// migrateFresh drops everything this project owns and applies the migrations,
// so each test starts from a known schema rather than from whatever the
// previous test left behind.
func migrateFresh(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("resetting the schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS udd_raw CASCADE`); err != nil {
		t.Fatalf("resetting udd_raw: %v", err)
	}

	migrations, err := subFS("db/migrations")
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}
	if err := store.Migrate(ctx, pool, migrations, discardLogger()); err != nil {
		t.Fatalf("migrating: %v", err)
	}
}

// seedUpstream stands in for the extract step. The loader reads udd_raw with
// ordinary SQL and does not care whether those tables arrived over a foreign
// data wrapper or were created here, which is what makes the transform and swap
// testable without a 2.4 GiB dump.
func seedUpstream(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	statements := []string{
		`CREATE SCHEMA udd_raw`,
		`CREATE TABLE udd_raw.packages (
			package text, version text, architecture text, source text, source_version text,
			essential text, depends text, recommends text, suggests text, enhances text,
			pre_depends text, breaks text, installed_size bigint, homepage text, size bigint,
			replaces text, section text, priority text, provides text, conflicts text,
			description text, distribution text, release text, component text
		)`,
		`CREATE TABLE udd_raw.sources (
			source text, version text, maintainer_name text, maintainer_email text,
			uploaders text, standards_version text, homepage text, priority text, section text,
			distribution text, release text, component text,
			vcs_type text, vcs_url text, vcs_browser text
		)`,
		`CREATE TABLE udd_raw.bugs (
			id integer, package text, source text, arrival timestamp, status text, severity text,
			submitter_name text, owner text, title text, last_modified timestamp, forwarded text,
			affects_stable boolean, affects_testing boolean, affects_unstable boolean,
			affects_experimental boolean
		)`,
		`CREATE TABLE udd_raw.archived_bugs (LIKE udd_raw.bugs)`,
		`CREATE TABLE udd_raw.bugs_tags (id integer, tag text)`,
		`CREATE TABLE udd_raw.popcon (package text, insts integer, vote integer, recent integer)`,

		// One package present in a suite we model, one in a suite we do not, and
		// one from Ubuntu. Only the first may survive the transform.
		`INSERT INTO udd_raw.packages
			(package, version, architecture, source, description, distribution, release, component, section)
		 VALUES
			('bash', '5.3-3', 'amd64', 'bash', E'GNU Bourne Again SHell\n The shell.', 'debian', 'sid', 'main', 'shells'),
			('bash', '5.2-1', 'amd64', 'bash', E'GNU Bourne Again SHell\n The shell.', 'debian', 'trixie', 'main', 'shells'),
			('ignored', '1.0', 'amd64', 'ignored', 'in a suite we do not model', 'debian', 'sid-backports', 'main', 'misc'),
			('ubuntu-only', '1.0', 'amd64', 'ubuntu-only', 'from ubuntu', 'ubuntu', 'noble', 'main', 'misc')`,

		`INSERT INTO udd_raw.sources (source, version, maintainer_name, distribution, release, component)
		 VALUES ('bash', '5.3-3', 'Matthias Klose', 'debian', 'sid', 'main')`,

		`INSERT INTO udd_raw.bugs (id, package, source, arrival, status, severity, title, last_modified)
		 VALUES
			(1, 'bash', 'bash', '2026-05-13 10:09:03', 'pending', 'minor', 'a bug', '2026-05-14 00:00:00'),
			(2, 'bash', 'bash', '2026-01-01 00:00:00', 'done', 'grave', 'a closed bug', '2026-02-01 00:00:00')`,

		`INSERT INTO udd_raw.archived_bugs (id, package, source, arrival, status, severity, title)
		 VALUES (3, 'bash', 'bash', '2020-01-01 00:00:00', 'done', 'normal', 'an archived bug')`,

		`INSERT INTO udd_raw.bugs_tags (id, tag) VALUES (1, 'upstream'), (99, 'orphan-tag')`,

		`INSERT INTO udd_raw.popcon (package, insts, vote, recent) VALUES ('bash', 200000, 1000, 50)`,
	}

	for _, statement := range statements {
		if _, err := pool.Exec(t.Context(), statement); err != nil {
			t.Fatalf("seeding upstream: %v\nstatement: %s", err, statement)
		}
	}
}

func subFS(directory string) (fstest.MapFS, error) {
	entries, err := sourceslist.SQL.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	files := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := sourceslist.SQL.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, err
		}
		files[entry.Name()] = &fstest.MapFile{Data: content}
	}
	return files, nil
}

func newLoader(t *testing.T, pool *pgxpool.Pool) *etl.Loader {
	t.Helper()

	transforms, err := subFS("db/etl/transform")
	if err != nil {
		t.Fatalf("reading transforms: %v", err)
	}
	seed, err := subFS("db/seed")
	if err != nil {
		t.Fatalf("reading seed: %v", err)
	}
	return etl.NewLoader(pool, transforms, seed, discardLogger())
}

func count(t *testing.T, pool *pgxpool.Pool, statement string) int {
	t.Helper()

	var total int
	if err := pool.QueryRow(t.Context(), statement).Scan(&total); err != nil {
		t.Fatalf("counting with %q: %v", statement, err)
	}
	return total
}

func TestLoadPublishesTransformedData(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	rows, err := newLoader(t, pool).Load(t.Context())
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if rows == 0 {
		t.Fatal("Load reported no rows published")
	}

	if got := count(t, pool, `SELECT count(*) FROM binary_packages WHERE name = 'bash'`); got != 2 {
		t.Errorf("bash binary rows = %d, want 2 (one per modelled suite)", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM source_packages WHERE name = 'bash'`); got != 1 {
		t.Errorf("bash source rows = %d, want 1", got)
	}
}

// UDD carries Ubuntu alongside Debian, and suites we do not model. Both must be
// dropped by the transform rather than reaching a page.
func TestLoadExcludesUbuntuAndUnmodelledSuites(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM binary_packages WHERE name = 'ubuntu-only'`); got != 0 {
		t.Errorf("ubuntu rows = %d, want 0", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM binary_packages WHERE name = 'ignored'`); got != 0 {
		t.Errorf("rows from an unmodelled suite = %d, want 0", got)
	}
}

// The suite join accepts either the role name or the codename, because UDD uses
// both. If this breaks, everything silently loads zero rows.
func TestLoadResolvesCodenamesToSuiteNames(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM binary_packages WHERE suite = 'unstable'`); got != 1 {
		t.Errorf("rows in unstable = %d, want 1 (sid should resolve to unstable)", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM binary_packages WHERE suite = 'stable'`); got != 1 {
		t.Errorf("rows in stable = %d, want 1 (trixie should resolve to stable)", got)
	}
}

func TestLoadDerivesBugStateFromStatus(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM bugs WHERE number = 2 AND done`); got != 1 {
		t.Error("a bug with status 'done' should be marked done")
	}
	if got := count(t, pool, `SELECT count(*) FROM bugs WHERE number = 3 AND archived`); got != 1 {
		t.Error("archived bugs should be loaded and marked archived")
	}
	if got := count(t, pool, `SELECT count(*) FROM bugs WHERE number = 1 AND NOT done AND NOT archived`); got != 1 {
		t.Error("an open bug should be neither done nor archived")
	}
}

// UDD stores naive timestamps holding UTC. Without the explicit AT TIME ZONE
// the value shifts by the server's offset, which is invisible unless asserted.
func TestLoadInterpretsUpstreamTimestampsAsUTC(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	var arrival string
	err := pool.QueryRow(
		t.Context(),
		`SELECT to_char(arrival AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') FROM bugs WHERE number = 1`,
	).Scan(&arrival)
	if err != nil {
		t.Fatalf("reading arrival: %v", err)
	}
	if arrival != "2026-05-13 10:09:03" {
		t.Errorf("arrival = %q, want it preserved as UTC", arrival)
	}
}

// A tag pointing at a bug that did not survive the transform would render a
// dead link, so the transform restricts tags to bugs it actually loaded.
func TestLoadDropsTagsForUnknownBugs(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM bug_tags WHERE bug_number = 99`); got != 0 {
		t.Error("a tag for a bug that was not loaded should be dropped")
	}
	if got := count(t, pool, `SELECT count(*) FROM bug_tags WHERE bug_number = 1`); got != 1 {
		t.Error("a tag for a loaded bug should survive")
	}
}

func TestLoadBuildsTheSearchTable(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	// Popularity has to reach the search table, because it is the whole ranking.
	var popularity int
	err := pool.QueryRow(
		t.Context(),
		`SELECT popularity FROM package_search WHERE name = 'bash' AND kind = 'binary' AND suite = 'unstable'`,
	).Scan(&popularity)
	if err != nil {
		t.Fatalf("reading the search row: %v", err)
	}
	if popularity != 200000 {
		t.Errorf("popularity = %d, want 200000 from popcon", popularity)
	}

	// The rebuilt indexes have to be usable, which a plain row count would not
	// notice: the search query is the reason the table exists.
	if got := count(t, pool, `SELECT count(*) FROM package_search
		WHERE suite = 'unstable' AND document @@ websearch_to_tsquery('english', 'shell')`); got == 0 {
		t.Error("full-text search found nothing, so the tsvector or its index is wrong")
	}
}

// Running twice over the same upstream snapshot must leave the same result. If
// this fails, a retried sync corrupts what is already published.
func TestLoadIsIdempotent(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	loader := newLoader(t, pool)

	first, err := loader.Load(t.Context())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := loader.Load(t.Context())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if first != second {
		t.Errorf("first load published %d rows, second published %d", first, second)
	}
	if got := count(t, pool, `SELECT count(*) FROM binary_packages WHERE name = 'bash'`); got != 2 {
		t.Errorf("bash rows after two loads = %d, want 2", got)
	}
}

// The swap must clear the overlay, because the batch it just published
// supersedes whatever the delta sync had recorded.
func TestLoadClearsTheFreshnessOverlay(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	_, err := pool.Exec(t.Context(),
		`INSERT INTO fresh_binary_versions (name, architecture, suite, version, observed_at)
		 VALUES ('bash', 'amd64', 'unstable', '5.4-1', now())`)
	if err != nil {
		t.Fatalf("seeding the overlay: %v", err)
	}

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM fresh_binary_versions`); got != 0 {
		t.Errorf("overlay rows after a bulk load = %d, want 0", got)
	}
}

// Nothing may be left behind for the next run to trip over.
func TestLoadLeavesNoStagingTables(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	leftovers := count(t, pool, `SELECT count(*) FROM pg_tables
		WHERE schemaname = 'public' AND (tablename LIKE '%\_\_new' OR tablename LIKE '%\_\_old')`)
	if leftovers != 0 {
		t.Errorf("%d staging or retired tables left behind", leftovers)
	}
}

// Popcon counts binary installations, so a source package whose binaries carry
// other names would score zero if its name were joined against popcon directly.
func TestLoadDerivesSourcePopularityFromItsBinaries(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	statements := []string{
		`INSERT INTO udd_raw.sources (source, version, maintainer_name, distribution, release, component)
		 VALUES ('srconly', '1.0-1', 'A Maintainer', 'debian', 'sid', 'main')`,
		`INSERT INTO udd_raw.packages
			(package, version, architecture, source, description, distribution, release, component, section)
		 VALUES ('srconly-bin', '1.0-1', 'amd64', 'srconly', 'built from srconly', 'debian', 'sid', 'main', 'misc')`,
		`INSERT INTO udd_raw.popcon (package, insts, vote, recent) VALUES ('srconly-bin', 4242, 10, 5)`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(t.Context(), statement); err != nil {
			t.Fatalf("seeding the source-only package: %v", err)
		}
	}

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	var popularity int
	err := pool.QueryRow(
		t.Context(),
		`SELECT popularity FROM package_search WHERE name = 'srconly' AND kind = 'source' AND suite = 'unstable'`,
	).Scan(&popularity)
	if err != nil {
		t.Fatalf("reading the source search row: %v", err)
	}
	if popularity != 4242 {
		t.Errorf("source popularity = %d, want 4242 from its binary's popcon", popularity)
	}
}

// Renaming a table does not rename its indexes, so without the explicit rename
// in the swap every load would leave live tables with staging-named indexes.
func TestLoadLeavesNoStagingIndexNames(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	loader := newLoader(t, pool)
	for range 2 {
		if _, err := loader.Load(t.Context()); err != nil {
			t.Fatalf("loading: %v", err)
		}
	}

	leftovers := count(t, pool, `SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND indexname LIKE '%\_\_new%'`)
	if leftovers != 0 {
		t.Errorf("%d indexes still carry a staging name after the swap", leftovers)
	}
}

// An earlier swap design renamed outgoing tables to `<table>__old` and dropped
// them after commit, so a crash in the gap left a table behind that made every
// later run fail. The swap must clean such leftovers up rather than trip over
// them.
func TestLoadSurvivesALeftoverRetiredTable(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)
	seedUpstream(t, pool)

	if _, err := pool.Exec(t.Context(), `CREATE TABLE bugs__old (LIKE bugs INCLUDING ALL)`); err != nil {
		t.Fatalf("planting a leftover retired table: %v", err)
	}

	if _, err := newLoader(t, pool).Load(t.Context()); err != nil {
		t.Fatalf("loading over a leftover retired table: %v", err)
	}

	leftovers := count(t, pool, `SELECT count(*) FROM pg_tables
		WHERE schemaname = 'public' AND tablename LIKE '%\_\_old'`)
	if leftovers != 0 {
		t.Errorf("%d retired tables left behind", leftovers)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool := requireDatabase(t)
	migrateFresh(t, pool)

	migrations, err := subFS("db/migrations")
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}

	// The second call must be a no-op rather than an error about existing tables.
	if err := store.Migrate(context.Background(), pool, migrations, discardLogger()); err != nil {
		t.Fatalf("migrating a second time: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM schema_migrations`); got != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", got)
	}
}

# Contributing

How to build, run, test, and deploy `sourcesli.st`. [README.md](README.md) says what the site is for, and [engineering/README.md](engineering/README.md) holds the principles, the strictness rules, the dependency budget, and the commit conventions this repository holds itself to. Read that one first.

## Architecture

Go for both binaries, Postgres for storage, server-rendered HTML with [htmx](https://htmx.org/) for interactivity and Tailwind for styling. No SPA framework, no npm, no `node_modules`. Search works with JavaScript disabled; htmx upgrades it to autocompletion when available.

```
cmd/web            HTTP server
cmd/ingest         UDD sync job, applies migrations then runs the pipeline
internal/domain    package, bug, and severity types; parsing only, no IO
internal/images    the image catalogue; pure logic over Debian's directory layout
internal/etl       extract, transform, and load
internal/store     migrations, connection handling, sqlc-generated queries
internal/web       handlers and template rendering
db/migrations      forward-only numbered migrations
db/etl/extract     scratch schema and the UDD table list
db/etl/transform   the only SQL that knows UDD's column names
db/queries         hand-written SQL, input to sqlc
db/seed            suite metadata we own rather than derive
web/templates      html/template sources
web/styles         Tailwind input
web/assets         generated stylesheet and vendored htmx
nix/               package, images, checks, dev environment
```

### Routes

- `/` lists every image Debian publishes for these machines, grouped by hardware, with search above it. It contacts nothing: filenames and URLs are derived from Debian's directory layout and the point release ingest observed. `/images` redirects here.
- `/d/{image}` is one image: what it lands as, and the checksum and signature to check it against.
- `/search` searches binary and source packages, with htmx autocompletion over a form that works without it, and offers near names when a query matches nothing.
- `/p/{suite}/{name}` shows versions per architecture, dependencies, the source package, and bugs filterable by status and severity. Source-only packages get the same page built from their source record.
- `/b/{number}` redirects to the bug in the BTS, which holds the live record a page here could only snapshot.
- `/healthz` answers 200 while the database is reachable. An empty database is healthy; an unreachable one is not.

## Development

Every system dependency comes from the flake. Nothing is installed on the host and nothing is assumed present on it.

```sh
nix develop
```

direnv users can instead create a git-ignored `.envrc` containing `use flake`.

The shell carries `just`, and the justfile names every command below. `just --list` is the index.

The database runs in a container; the web tier runs from source against it, so a code change costs a restart rather than an image build.

```sh
just dev
```

That starts the database, applies migrations and seed data, and serves on `http://127.0.0.1:8080`. It needs no network and is safe to rerun. Ctrl-c stops the server and leaves the database running, so the edit-and-restart loop is `just web`.

The two halves are separate recipes when you want them: `just up` for the database and schema alone, `just web` to serve against a database that is already up.

The database is published on `127.0.0.1:15432`, an uncommon port so it never loses a race against a system Postgres. It runs in a rootless podman pod on a private network, reachable on the loopback interface only, with state in a named volume.

A fresh database has the schema and the suites but no packages, so the site renders empty until you fill it.

### Data

A database with no packages renders an empty site. Three ways to fill it, cheapest first:

```sh
just restore   # load the newest snapshot from .dev/snapshots: minutes, offline
just ingest    # one real ETL pass; the first downloads 2.4 GiB into .dev/cache
just snapshot  # write the current database to a new timestamped snapshot
```

Snapshots are timestamped and never overwritten, so taking one can't destroy an earlier one. `just restore` picks the newest unless you name a file.

### Stopping and destroying

```sh
just stop   # stop the containers, keeping the database
just down   # destroy containers, volumes, and images; prompts first
```

`just down` deletes everything ingested. It asks before doing so, and `--yes` skips the question. It leaves `.dev/` alone, so the dump cache and your snapshots survive a teardown.

### The container stack

`just up` does not run the web tier in a container. To check the image that actually ships:

```sh
just stack   # builds the images, runs web in a container on http://127.0.0.1:18080
```

### Checks

```sh
just fmt     # format everything: Go, Nix, shell, YAML, TOML, SQL
just check   # format check, lint, tests; exactly what CI runs
```

`just test` and `just lint` are the tighter loop, and `just vuln` runs the scan CI keeps outside `nix flake check`.

Tests come at three levels: pure tests for the domain and the ISO chooser, handler tests against the real router and templates with a fake query layer, and ETL tests against a real Postgres. The ETL tests are skipped without a database, because what they check is what a fake cannot judge: that the transform SQL runs, the indexes rebuild, and the swap publishes.

```sh
just test-all   # every test, ETL included
```

That runs the ETL tests against a `sourceslist_test` database it creates beside the development one, because they drop and recreate the `public` schema. Never point `TEST_DATABASE_URL` at a database whose contents you want.

### Generated files

`internal/store/query` (sqlc, from `db/queries`) and `web/assets/app.css` (Tailwind, from `web/styles` and the templates) are committed tool output: `go:embed` cannot reach a file that is not there. After changing their inputs, regenerate inside the dev shell:

```sh
just generate
```

CI regenerates both and fails on any diff. The formatter excludes them, so they stay byte-exact tool output.

### Upstream

Ingest is a separate, deliberate step because it downloads the whole UDD dump, 2.4 GiB as of August 2026. A second run against an unchanged dump transfers no body and rebuilds nothing, unless the tables it published are gone, in which case it rebuilds them rather than reporting success over an empty site.

Tests and CI never contact Debian: `internal/images` and `internal/domain` are pure, and the ETL is exercised against a restored dump rather than against upstream.

## Deployment

Images are built by Nix, not by `Dockerfile`s:

```sh
nix build .#images-web      # 9.3 MB: the binary, CA certificates, a passwd entry
nix build .#images-ingest   # 64 MB: adds the Postgres client tools the restore needs
```

Neither image contains a shell or a package manager.

### Configuration

Both binaries read the environment and refuse to start when anything required is missing.

| Variable | Binary | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | both | Postgres connection string. The web tier needs only a read-only role; ingest owns every write. |
| `LISTEN_ADDRESS` | web | Defaults to `127.0.0.1:8080`. |
| `UDD_DUMP_URL` | ingest | Where the UDD dump is published. Deliberately no default. |
| `UDD_CACHE_DIR` | ingest | Where the downloaded dump lives between runs. Losing it is harmless. |
| `UDD_SCRATCH_DATABASE` | ingest | Database the dump restores into. Dropped and recreated every run. |

The ingest role must be able to create and drop the scratch database, create the `pg_trgm`, `fuzzystrmatch`, and `postgres_fdw` extensions, and define a foreign server. Grant exactly that; do not run it as a superuser for convenience.

Ingest is a job, not a service. Scheduling, retries, and backoff belong to whatever runs it, never faster than upstream publishes.

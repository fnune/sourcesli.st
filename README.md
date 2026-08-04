# `sourcesli.st`

An alternative way to browse Debian packages and bug reports, and to find the right Debian ISO to download.

Site: [sourcesli.st](https://sourcesli.st). Source: [github.com/fnune/sourcesli.st](https://github.com/fnune/sourcesli.st).

## How it works

Debian already aggregates the data this site needs. The [Ultimate Debian Database](https://wiki.debian.org/UltimateDebianDatabase) imports the archive's `Packages` and `Sources` indices, bugs from the BTS, popcon, upload history, and more into one Postgres database, and publishes it for bulk consumers.

So the shape of the system is simple:

```
udd.debian.org/dumps/udd.dump ──(conditional GET, every two days)──> ingest ──> our Postgres <──> web
```

- **ingest** is a scheduled job. It conditionally fetches the UDD dump, restores only the tables it needs into a scratch database, maps them into our own schema, and swaps the result in atomically. It runs on a cadence matched to UDD's publication schedule, and an unchanged dump makes it a no-op.
- **our Postgres** holds everything the site serves, including the search and autocompletion indexes.
- **web** is a Go server rendering HTML. It reads only from our database.

The property that matters: a user request never causes a request to a Debian service, so upstream load stays constant whether the site has ten visitors or ten million. [Decision record 0001](engineering/decisions/0001-source-bulk-data-from-udd.md) explains why the dump beats every other source, and the [engineering principles](engineering/README.md) hold the rules ingest follows.

## Architecture

Go for both binaries, Postgres for storage, server-rendered HTML with [htmx](https://htmx.org/) for interactivity and Tailwind for styling. No SPA framework, no npm, no `node_modules`. Search works with JavaScript disabled; htmx upgrades it to autocompletion when available.

```
cmd/web            HTTP server
cmd/ingest         UDD sync job, applies migrations then runs the pipeline
internal/domain    package, bug, and severity types; parsing only, no IO
internal/images    the ISO chooser; pure logic over Debian's directory layout
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

### Pages

- `/` and `/search` search binary and source packages, with htmx autocompletion over a form that works without it.
- `/p/{suite}/{name}` shows versions per architecture, dependencies, the source package, and bugs filterable by status and severity. Source-only packages get the same page built from their source record.
- `/b/{number}` shows one bug and links to the BTS for its live state.
- `/images` is the ISO chooser. It contacts nothing: the recommendation is derived from the answers and Debian's stable directory layout.
- `/healthz` answers 200 while the database is reachable. An empty database is healthy; an unreachable one is not.

## Development

Every system dependency comes from the flake. Nothing is installed on the host and nothing is assumed present on it.

```sh
nix develop
```

direnv users can instead create a git-ignored `.envrc` containing `use flake`.

Start the whole environment, building images and all:

```sh
nix run .#dev-up
```

The site is then on `http://127.0.0.1:8080`. Services run in a rootless podman pod on a private network, published only on the loopback interface, with state in named volumes.

Destroy it completely, leaving no trace on the host:

```sh
nix run .#dev-down
```

### Checks

```sh
nix fmt           # format everything: Go, Nix, shell, YAML, TOML, SQL
nix flake check   # format check, lint, tests; exactly what CI runs
```

Inside the dev shell, `go test ./...` and `golangci-lint run` work as usual for a tighter loop.

Tests come at three levels: pure tests for the domain and the ISO chooser, handler tests against the real router and templates with a fake query layer, and ETL tests against a real Postgres. The ETL tests are skipped without a database, because what they check is what a fake cannot judge: that the transform SQL runs, the indexes rebuild, and the swap publishes.

```sh
export TEST_DATABASE_URL='postgres://user@host/database'
go test ./... -count=1
```

Any Postgres with the `pg_trgm` and `postgres_fdw` extensions works. The tests drop and recreate the `public` schema, so point them at a disposable database.

### Generated files

`internal/store/query` (sqlc, from `db/queries`) and `web/assets/app.css` (Tailwind, from `web/styles` and the templates) are committed tool output: `go:embed` cannot reach a file that is not there. After changing their inputs, regenerate inside the dev shell:

```sh
sqlc generate
tailwindcss --input web/styles/app.css --output web/assets/app.css --minify
```

CI regenerates both and fails on any diff. The formatter excludes them, so they stay byte-exact tool output.

### Data

The database is empty until it is filled. Ingest is a separate, deliberate step because it downloads the whole UDD dump, 2.4 GiB as of August 2026:

```sh
nix run .#ingest
```

A second run against an unchanged dump transfers no body and rebuilds nothing.

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

The ingest role must be able to create and drop the scratch database, create the `pg_trgm` and `postgres_fdw` extensions, and define a foreign server. Grant exactly that; do not run it as a superuser for convenience.

Ingest is a job, not a service. Scheduling, retries, and backoff belong to whatever runs it, never faster than upstream publishes.

## Contributing

Read [engineering/README.md](engineering/README.md) first. It covers the principles, the strictness rules, the dependency budget, and the commit conventions this repository holds itself to.

## Licence and attribution

The data this site presents comes from Debian and remains under its original licences. Every page reproducing upstream content links back to the authoritative Debian page. `sourcesli.st` is not affiliated with the Debian Project.

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

We deliberately use the published dump rather than UDD's read-only mirror. A static file costs Debian the same no matter how badly we write our code; a query against the shared mirror spends CPU and connections that other people are waiting on. The mirror is for humans exploring interactively, and nothing automated touches it.

The property that matters: a user request never causes a request to a Debian service. Upstream load is constant whether the site has ten visitors or ten million. See [decision record 0001](engineering/decisions/0001-source-bulk-data-from-udd.md) for why this rules out the more obvious approaches, and the Debian data citizenship section of the [engineering principles](engineering/README.md) for the rules ingest follows.

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
- `/p/{suite}/{name}` shows versions per architecture, the source package, and open bugs.
- `/b/{number}` shows one bug and links to the BTS for its live state.
- `/images` is the ISO chooser. It contacts nothing: the recommendation is derived from the answers and Debian's stable directory layout.

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
nix fmt           # format every git-checked file: Go, Nix, Markdown, YAML, shell
nix flake check   # format check, lint, vulnerability scan, tests
```

`nix flake check` is exactly what CI runs. Inside the dev shell, `go test ./...` and `golangci-lint run` work as usual for a tighter loop.

Tests come at three levels. The domain and the ISO chooser are pure and always run. The handler tests exercise the real router, the real templates, and real rendering against an in-memory fake of the query interface. The ETL tests need a real Postgres and are skipped without one, because the things most likely to be wrong there are the things a fake cannot have an opinion about: whether the transform SQL is valid, whether the rebuilt indexes are usable, and whether the rename swap actually publishes.

```sh
export TEST_DATABASE_URL='postgres://user@host/database'
go test ./... -count=1
```

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

## Contributing

Read [engineering/README.md](engineering/README.md) first. It covers the principles, the strictness rules, the dependency budget, and the commit conventions this repository holds itself to.

## Licence and attribution

The data this site presents comes from Debian and remains under its original licences. Every page reproducing upstream content links back to the authoritative Debian page. `sourcesli.st` is not affiliated with the Debian Project.

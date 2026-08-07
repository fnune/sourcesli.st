# Engineering rules

Rules for the whole repository. [README.md](../README.md) says what the product does; this file says how we build it.

---

## The site stays calm

This covers the code, the copy, the defaults, and what happens when things go wrong.

- A feature has to earn its place or it does not ship.
- Plain words and predictable defaults.
- No dark patterns, no shame buttons, no ads, no nagging.
- The user stays in control. Do not coerce, guilt, or hide options.
- Error messages are accurate and plain, and never blame the user.
- The site works without JavaScript. Client-side code only improves what is already there.

## Going easy on Debian

Debian's hardware and bandwidth are paid for by donations and run by volunteers, so how much load we put on them is a design constraint, not an afterthought.

- A user request never causes a request to a Debian service. Our upstream load stays the same however much traffic we get.
- Automated jobs read one source: the UDD dump at `https://udd.debian.org/dumps/udd.dump`. It is a static file, so what it costs Debian does not change with what we do to it.
- Fetch the dump conditionally, sending the stored `ETag` and `Last-Modified`. An unchanged dump must transfer zero body bytes.
- Never sync faster than upstream publishes. The dump is regenerated every two days.
- Restore selectively with `pg_restore --table`. Never unpack all of UDD to read a fraction of it.
- The rule is about cost, not about hostnames: no automated job may put unbounded or bulk load on shared Debian infrastructure. The read-only mirror at `udd-mirror.debian.net` fails that test, because a slow query there costs other people CPU and connections, so nothing automated queries it. A human exploring it stays polite: read-only, one connection, a statement timeout on every query.
- Never scrape. `packages.debian.org`, `sources.debian.org`, `tracker.debian.org`, and the BTS web interface are for humans. Their data is in UDD.
- Identify every connection with this exact string, as the HTTP `User-Agent` and the Postgres `application_name`:

    ```
    sourcesli.st/<version> (+https://github.com/fnune/sourcesli.st)
    ```

    The repository link is how an operator who wants us to stop can reach us. Never trim it, never replace it with a generic client string.
- Back off on failure: exponential with jitter, honour `Retry-After`, and give up rather than retry in a loop. Ingest is a single-shot job, so the scheduler owns this.
- Stale data is always better than more upstream load. Show how stale it is; never hide it.

If we ever need something UDD does not carry, it comes from `deb.debian.org`, which is a CDN built to be polled by every `apt update` on every Debian machine. Not from the mirror, and not from the BTS. Do not reimplement `InRelease` verification or pdiff handling either: `apt-get update` in a container already does both correctly. `fresh_binary_versions` is an empty table waiting for that: package versions polled more often than the dump, kept separate so a failure there leaves the site at dump freshness instead of breaking it. Nothing writes to it yet.

## Dependency injection

Reference: Seemann, [*Dependency Injection Principles, Practices, and Patterns*](https://www.manning.com/books/dependency-injection-principles-practices-patterns).

- Constructor injection: a `New...` function takes every dependency and returns a value ready to use. No package-level mutable state, no `init()` wiring.
- Inject the things that vary: IO, time, randomness, configuration. Do not inject stdlib primitives, value objects, or pure functions.
- `main` wires everything up and is the only place that knows concrete implementations. No container, no reflection.
- No service locator, no ambient context. `time.Now` and environment lookups arrive as parameters; a clock is a one-method interface.
- If a constructor's argument list keeps growing, the type does too much.
- Tests use the real thing where it is stable and hand-written fakes where it is not, with integration tests against a real Postgres. Do not mock what you own. The UDD client runs against recorded fixtures, never against `udd-mirror.debian.net`.

## Layers own their interfaces

- Split into view, domain, and data access. The interface belongs to whoever uses it: the domain declares the storage interfaces it needs, and the data layer implements them.
- Layers do not import the next one down.
- `internal/` holds everything not meant for outside use, which is nearly all of it.

## Types

- Make illegal states impossible to write down. Use named types where a primitive could be swapped by mistake: `PackageName`, `SuiteName`, `BugNumber`.
- No `any` in domain code. Where a library forces it, parse into a domain type straight away.
- No unchecked type assertions. `forcetypeassert` is enabled.
- Parse, do not validate. A constructor at a boundary returns a domain type or an error, never a raw value plus a boolean. Do this at every boundary: HTTP, environment, UDD rows, files. Code further in can then trust the type.
- Handle every error. `errcheck` runs with no ignore list. Wrap with `%w` and say what was attempted with what inputs.
- `panic` only for programmer errors at startup. Never for anything a request or an upstream row can reach.
- A `//nolint` needs a comment naming a real constraint.

## Data and SQL

- Migrations are forward-only, numbered, checked in, and applied by one command.
- Our schema is ours. UDD's shape is an input, not a contract, so an upstream rename breaks one adapter and nothing else.
- SQL is hand-written in `.sql` files. sqlc generates typed Go from them. No ORM, no query building at runtime.
- Every web query has a bounded result set and an index that serves it. Run `EXPLAIN` before shipping a new query shape.
- Ingest never changes rows the web tier is reading. Build in staging tables, then publish in one transaction.
- Ingest is idempotent, and records which upstream snapshot produced the current data.

## Comments

- Comments are a last resort. Make the code say it instead.
- A comment explains why, when the why is not obvious: a constraint, an invariant, a workaround. A comment that repeats the code is noise.
- Doc comments on exported identifiers follow the Go convention and describe the contract, not the implementation.

## Dependencies

- Start with the standard library. It covers most of this project.
- Before adding one, ask: could the standard library do it, how big is its own dependency tree, is it maintained, and does whoever runs it answer to users or to a vendor's hosted product.
- Under 50 lines: write it. Under 100 lines and external: vendor it, with attribution and its licence.
- Fewer than 10 direct runtime modules in `go.mod`. Going over needs a written reason. Build and test tools do not count, but still get checked.
- Pin exactly. `go.mod`, `go.sum`, and `flake.lock` are committed and nothing floats. Renovate proposes updates and humans review them: weekly batch, 7 day minimum release age, no auto-merge, security advisories same-day.
- No npm and no `node_modules`, in development, CI, or images. A front-end dependency is either a vendored file we have read or a standalone binary from the flake.
- Assets are built into the binary. No CDN sees our users.

## Running it

- No silent catches. Errors say what was attempted, with what inputs, and what failed. Boundaries decide what the user sees; layers underneath return errors.
- Structured logs via `log/slog`. Every sync run records what it fetched, how long it took, how many rows changed, and which snapshot it read.
- `context.Context` goes through every IO path and carries the deadline. No unbounded waits.
- Secrets never appear in code or committed files. The web tier connects with a read-only role and ingest is the only writer. `govulncheck` runs in CI.
- Privacy is a constraint: no accounts, no third-party scripts, no identifying analytics, no full IP addresses in logs, no cookies beyond the strictly necessary.
- Every page that shows Debian content links back to the Debian page it came from.

## Checks before pushing

- `nix fmt` formats every file type treefmt covers: Go, Nix, shell, YAML, TOML, and SQL.
- `nix flake check` runs the whole baseline, and is exactly what CI runs.
- While iterating, narrow it down: `go test ./internal/etl/...`, `golangci-lint run ./internal/etl/...`.

## Git

- [Conventional Commits](https://www.conventionalcommits.org/): `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`, `revert`. Scope optional and lowercase.
- Imperative mood. Subject under 72 characters. Body wraps at 80 and explains why when the diff does not.
- No value judgements ("improve", "cleaner", "robust") and no vague qualifiers ("various", "ensure").
- No AI attribution and no Co-Authored-By for AI tools.
- One commit per PR by default. Squash on merge. Split larger changes where they come apart naturally.

## CI

- Every PR runs format check, lint, tests, and a vulnerability scan, through the same `nix flake check` a contributor runs.
- One workflow, `.github/workflows/ci.yml`. It installs Nix; the flake holds every tool version.
- CI never connects to Debian. Integration tests run against a Postgres container and checked-in fixtures.

## One command to set up, one to tear down

- `flake.nix` and `flake.lock` are the only place system dependencies are named, for developers, CI, and images alike. If a build needs something from the host, that is a bug.
- `nix run .#dev-up` starts everything. `nix run .#dev-down` removes every trace: containers, volumes, network, generated state.
- The dev environment runs the same OCI images Nix builds for deployment, from the same derivations.
- direnv users add a git-ignored `.envrc` containing `use flake`.

## Tools

- Nix flakes for system dependencies, the dev shell, OCI images, and CI. Images come from `pkgs.dockerTools.buildLayeredImage`; no `Dockerfile`s. The compose file is generated from Nix and run under rootless podman.
- Go for both binaries. Postgres for storage, full-text search, and trigram indexes, matching UDD's major version where practical. No separate search engine until Postgres demonstrably cannot do the job.
- [pgx](https://github.com/jackc/pgx) as the driver, [sqlc](https://sqlc.dev/) for typed queries.
- Server-rendered `html/template`, embedded with `embed`. No SPA framework. The server sends HTML, never JSON for a client-side renderer.
- [htmx](https://htmx.org/) vendored as one file we have read. Every htmx interaction sits on top of a form or link that works without JavaScript.
- [Tailwind CSS](https://tailwindcss.com/) via the standalone binary, at build time only. No npm, no PostCSS.
- The standard `testing` package. [golangci-lint](https://golangci-lint.run/) lints; [treefmt](https://treefmt.com/) formats every covered file type in one pass.

## Writing

- Sentence-case headings. Bold sparingly.
- Short sentences, active voice. Prefer must, can, and will over should, may, and might.
- No em-dashes: use a colon, comma, semicolon, or full stop.
- No AI attribution in commits or code.

---

## Use of AI and LLMs

LLMs may write code here. The output is held to every rule above: tests, types, dependencies, and review.

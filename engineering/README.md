# Engineering principles

Rules that bind the whole repository. [README.md](../README.md) says what the product does; this file says how we build it.

---

## Principles

### Calm app

`sourcesli.st` must feel calm. This binds the code, the copy, the defaults, and the failure paths.

- A feature earns its place or it does not ship.
- Plain copy, ordinary words, predictable defaults. Clarity beats cleverness.
- No dark patterns, no shame buttons, no ads, no nagging.
- The user stays in control. Do not coerce, guilt, or hide options.
- Error messages are accurate and plain. They never blame the user.
- The site works without JavaScript. Client-side code enhances; it is never the only path to content.

### Debian data citizenship

Volunteers pay for the data this project runs on, so upstream load is a design constraint.

- A user request never causes a request to a Debian service. Upstream load stays constant, whatever our traffic.
- Automated jobs read one source: the UDD dump at `https://udd.debian.org/dumps/udd.dump`.
- Fetch the dump conditionally, with the stored `ETag` and `Last-Modified`. An unchanged dump must transfer zero body bytes.
- Never sync faster than upstream publishes. The dump regenerates every two days.
- Restore selectively with `pg_restore --table`. Never materialise all of UDD to read a fraction of it.
- The rule is cost shape, not hostname: no automated job may place unbounded or bulk load on shared Debian infrastructure. The mirror at `udd-mirror.debian.net` fails that test, so nothing automated queries it. A human exploring it stays polite: read-only, one connection, a statement timeout on every query.
- Freshness between dumps comes from `deb.debian.org`, the CDN built to be polled. Never from the mirror, never from the BTS. `fresh_binary_versions` is the overlay table that would hold those versions, kept separate from the bulk tables so that a failure there leaves the site at dump freshness. Nothing writes to it yet.
- Do not reimplement `InRelease` verification or pdiff handling. `apt-get update` in a container already does both correctly.
- Never scrape. `packages.debian.org`, `sources.debian.org`, `tracker.debian.org`, and the BTS web interface are for humans. Their data is in UDD.
- Identify every connection with this exact string, as the HTTP `User-Agent` and the Postgres `application_name`:

    ```
    sourcesli.st/<version> (+https://github.com/fnune/sourcesli.st)
    ```

    The repository link is the contact path for an operator who wants us to stop. Never trim it, never replace it with a generic client string.
- Back off on failure: exponential with jitter, honour `Retry-After`, give up rather than retry-storm. Ingest is a single-shot job, so this policy lives in the scheduler.
- Stale data beats upstream load, always. Show the staleness; never hide it.

### Dependency injection

Reference: Seemann, [*Dependency Injection Principles, Practices, and Patterns*](https://www.manning.com/books/dependency-injection-principles-practices-patterns).

- Constructor injection: a `New...` function takes every dependency and returns a ready value. No package-level mutable state, no `init()` wiring.
- Inject volatile dependencies: IO, time, randomness, runtime configuration, anything that does not yet exist. Do not inject stable ones: stdlib primitives, value objects, pure functions.
- `main` is the composition root and the only place that knows concrete implementations. Pure DI: hand-wired graphs, no container, no reflection.
- No service locator, no ambient context. `time.Now` and environment lookups arrive as parameters; a clock is a one-method interface.
- A growing constructor argument list means the type does too much.
- Tests follow the seams: real stable dependencies, hand-written fakes for volatile ones, integration tests against a real Postgres. Do not mock what you own. The UDD client runs against recorded fixtures, never against `udd-mirror.debian.net`.

### Layers own their abstractions

- Split into view, domain, and data access. The interface belongs to the consumer: the domain declares the storage interfaces it needs, and the data layer implements them.
- Reject naive layering, where each layer imports the next one down. This is the Dependency Inversion Principle applied at the module boundary.
- `internal/` holds everything not meant for outside consumption, which is nearly all of it.

### Strict typing

- Make illegal states unrepresentable. Named types where primitives could be swapped: `PackageName`, `SuiteName`, `BugNumber`.
- No `any` in domain code. Where a library forces it, parse into a domain type immediately.
- No unchecked type assertions. `forcetypeassert` is enabled.
- Parse, do not validate. A boundary constructor returns a domain type or an error, never a raw value plus a boolean. Parse at every boundary: HTTP, environment, UDD rows, files. Downstream code trusts the type.
- Handle every error. `errcheck` runs with no ignore list. Wrap with `%w` and say what was attempted with what inputs.
- `panic` only for programmer errors at startup. Never for anything a request or an upstream row can reach.
- A `//nolint` needs a comment naming a genuine constraint, or it does not survive review.

### Data and SQL

The schema is the design.

- Migrations are forward-only, numbered, checked in, and applied by one command.
- Our schema is ours. UDD's shape is an input, not a contract; an upstream rename breaks one adapter, not the application.
- SQL is hand-written in `.sql` files. sqlc generates typed Go from them. No ORM, no runtime query building.
- Every web query has a bounded result set and an index that serves it. `EXPLAIN` before shipping a new query shape.
- Ingest never mutates rows the web tier reads. Build in staging tables, publish inside one transaction.
- Ingest is idempotent, and it records which upstream snapshot produced the current data.

### Code comments

- Code is self-evident; comments are a last resort.
- A comment states a non-obvious why: a hidden constraint, an invariant, a workaround. A comment that restates the code is noise.
- Doc comments on exported identifiers follow the Go convention and state contract, not implementation.

### Dependency hygiene

- Start with the standard library. It covers most of this project.
- Vet every dependency: could the standard library do it, how big is the transitive tree, is it maintained, and does its governance serve users rather than a vendor's hosted product. Document exceptions in the PR.
- Under 50 lines: write it. Under 100 lines and external: vendor it, with attribution and its licence.
- Budget: fewer than 10 direct runtime modules in `go.mod`. Exceeding it requires written justification. Build and test tools do not count, but are still vetted.
- Pin exactly. `go.mod`, `go.sum`, and `flake.lock` are committed; nothing floats. Renovate proposes updates and humans review them: weekly batch, 7 day minimum release age, no auto-merge, security advisories same-day.
- No npm and no `node_modules`, in development, CI, or images. A front-end dependency is a vendored reviewed file or a standalone binary from the flake.
- Assets are embedded into the binary at build time. No CDN sees our users.

### Operational discipline

- No silent catches. Errors carry what was attempted, with what inputs, and what failed. Boundaries decide what the user sees; deeper layers return errors.
- Structured logs via `log/slog`. Every sync run records what it fetched, how long it took, how many rows changed, and which snapshot it read.
- `context.Context` threads through every IO path and carries the deadline. No unbounded waits.
- Secrets never appear in code or committed files. Least privilege: the web tier connects with a read-only role; ingest is the only writer. `govulncheck` runs in CI.
- Privacy is a design constraint: no accounts, no third-party scripts, no identifying analytics, no full IP addresses in logs, no cookies beyond the strictly necessary.
- Every page that reproduces Debian content links back to the authoritative Debian page.

### Local checks before pushing

- `nix fmt` formats every file type treefmt covers: Go, Nix, shell, YAML, TOML, and SQL.
- `nix flake check` runs the whole baseline and is exactly what CI runs.
- While iterating, scope down: `go test ./internal/etl/...`, `golangci-lint run ./internal/etl/...`.

### Git conventions

- [Conventional Commits](https://www.conventionalcommits.org/): `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`, `revert`. Scope optional and lowercase.
- Imperative mood. Subject under 72 characters. Body wraps at 80 and explains why when the diff does not.
- No value judgements ("improve", "cleaner", "robust") and no vague qualifiers ("various", "ensure").
- No AI attribution and no Co-Authored-By for AI tools.
- One commit per PR by default. Squash on merge. Split larger changes at natural seams.

### Continuous integration

- Every PR runs format check, lint, tests, and a vulnerability scan, through the same `nix flake check` a contributor runs.
- One workflow, `.github/workflows/ci.yml`. It installs Nix; the flake holds every tool version.
- CI never connects to Debian. Integration tests run against a Postgres container and checked-in fixtures.

### One-command environments

- `flake.nix` and `flake.lock` are the single source of every system dependency, for developers, CI, and images. A build that depends on anything outside the flake is a bug.
- `nix run .#dev-up` starts everything. `nix run .#dev-down` removes every trace: containers, volumes, network, generated state.
- The dev environment runs the same OCI images Nix builds for deployment, from the same derivations.
- direnv users add a git-ignored `.envrc` containing `use flake`.

### Toolchain

- Nix flakes for system dependencies, the dev shell, OCI images, and CI. Images come from `pkgs.dockerTools.buildLayeredImage`; no `Dockerfile`s. Orchestration is a compose file generated from Nix, run under rootless podman.
- Go for both binaries. Postgres for storage, full-text search, and trigram indexes, matching UDD's major version where practical. No separate search engine until Postgres demonstrably cannot do the job.
- [pgx](https://github.com/jackc/pgx) as the driver, [sqlc](https://sqlc.dev/) for typed queries.
- Server-rendered `html/template`, embedded with `embed`. No SPA framework. The server sends HTML, never JSON for a client-side renderer.
- [htmx](https://htmx.org/) vendored as one reviewed file. Every htmx interaction layers over a form or link that works with JavaScript disabled.
- [Tailwind CSS](https://tailwindcss.com/) via the standalone binary, at build time only. No npm, no PostCSS.
- The standard `testing` package. [golangci-lint](https://golangci-lint.run/) lints; [treefmt](https://treefmt.com/) formats every covered file type in one pass.

### Writing style

- Sentence-case headings. Bold sparingly.
- Short sentences, active voice, one meaning per word throughout a document. Prefer must, can, and will over should, may, and might.
- No em-dashes: use a colon, comma, semicolon, or period.
- No rhetorical triplets.
- No AI attribution in commits or code.

---

## Use of AI and LLMs

LLMs may write code here. The output is held to every standard above: tests, strict typing, dependency hygiene, and review. Nothing about the author changes the bar.

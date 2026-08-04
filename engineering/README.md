# Engineering principles

Principles, declarations, and shared conventions that apply across the repository. Not product-specific.

---

## Principles

### Calm app

`sourcesli.st` is built to feel calm. Simple, clear, predictable, respectful of user agency. Engineers carry this through to the code, the copy, the defaults, and the failure paths.

- Simplicity is the default. A feature earns its place or it does not ship.
- Clarity over cleverness. Plain copy, ordinary words, predictable defaults.
- Every interaction is calm. No dark patterns. No shame buttons ("Accept" / "Maybe later"). No ads. No nagging.
- Respect user agency. The user is in control. We do not coerce, guilt, or hide options.
- Failures are shown calmly. Error messages are accurate, plain, and never blame the user.
- The site works without JavaScript. Client-side code enhances; it is never the only path to the content.

### Debian data citizenship

This project exists on top of data that volunteers pay for. Upstream load is a first-class design constraint, not an afterthought.

- Upstream load is constant and independent of our traffic. A user request never causes a request to a Debian service. Everything is served from our own database.
- Bulk data comes from the [Ultimate Debian Database](https://wiki.debian.org/UltimateDebianDatabase) dump at `https://udd.debian.org/dumps/udd.dump`, 2.4 GiB as of August 2026 and growing, regenerated every two days. That is the only upstream source any automated job may touch.
- The dump is preferred over the public mirror at `udd-mirror.debian.net` because its cost to Debian is fixed and inert. A static file download costs the same no matter how badly we write our code, whereas a query against the shared mirror spends CPU, connections, and I/O that other people are waiting on. We would rather our mistakes cost us time than cost somebody else a service.
- The governing rule is cost shape, not hostname: an automated job may not place unbounded or bulk load on shared Debian infrastructure. The mirror fails that test for our workload, so nothing automated queries it. It is for humans exploring interactively, and if you use it, be a polite client: read-only, one connection, a statement timeout on every query, no long-running transactions.
- Freshness between dumps comes from `deb.debian.org`, never from the mirror and never from the BTS. Archive indices exist to be polled; that is what every `apt update` does. See [decision record 0003](decisions/0003-two-tier-freshness.md).
- We do not implement `InRelease` verification or pdiff application ourselves. `apt-get update` in a container with our own `sources.list` already does it correctly, and reimplementing OpenPGP verification to avoid a container trades real risk for no gain.
- Data that cannot be refreshed politely stays stale and says so. Bug views state the date they are from and link to the BTS for live state. Inventing a rude refresh path to close a two-day gap is not a trade we make.
- Never scrape. `packages.debian.org`, `sources.debian.org`, `tracker.debian.org`, and the BTS web interface are for humans. Their data is in UDD.
- Always issue the dump fetch as a conditional request, storing the `ETag` and `Last-Modified` from the previous run. An unchanged dump must transfer zero bytes of body.
- Never poll faster than the upstream data changes. The dump is regenerated every two days, so a sync schedule tighter than that buys nothing and costs someone else.
- Restore selectively. `pg_restore --table` pulls only the tables we map, so we never materialise all of UDD to read a fraction of it.
- Identify ourselves on every connection, using this exact string as the HTTP `User-Agent` and the Postgres `application_name`:

    ```
    sourcesli.st/<version> (+https://github.com/fnune/sourcesli.st)
    ```

    The repository link is the contact path: an operator who wants us to stop can open an issue without doing any detective work first. It is not decoration, so it is never trimmed to save bytes and never replaced with a generic client string.
- Back off on failure. Exponential backoff with jitter, honour `Retry-After`, and give up rather than retry-storm. A failed sync is not an emergency because we still hold the previous one. Ingest is a single-shot job, so this policy lives in the scheduler's retry configuration, not inside the binary.
- Serving stale data always beats hammering upstream. Staleness is surfaced to the user, not hidden.
- If we ever need archive indices directly, they come from `deb.debian.org` (the CDN, funded for this) and never from a volunteer country mirror, using conditional requests and pdiffs the way `apt` does.

### Testability via dependency injection

Reference: [Mark Seemann](https://blog.ploeh.dk/), [*Dependency Injection Principles, Practices, and Patterns*](https://www.manning.com/books/dependency-injection-principles-practices-patterns).

- Constructor Injection is the default: a `New...` function takes every dependency as a parameter and returns a ready value. No package-level mutable state, no `init()` wiring.
- Inject Volatile Dependencies: any dependency that introduces side effects (IO, database, file system, network), nondeterministic behaviour (time, randomness), a runtime configuration requirement, or that does not yet exist.
- Do not inject Stable Dependencies: deterministic, side-effect-free, already-installed types such as stdlib primitives, value objects, and pure functions.
- The Composition Root is the single location where the object graph is composed. It sits in `main`. Nothing else in the codebase knows about concrete implementations of Volatile Dependencies.
- Pure DI only: hand-wired object graphs in `main`. No DI container, no reflection-based wiring.
- Service Locator is an anti-pattern: it hides dependencies behind a runtime lookup. Receive them as constructor parameters instead.
- Ambient Context is an anti-pattern. `time.Now`, `rand`, and environment lookups are injected, not called from deep in the code. A clock is an interface with one method.
- A growing constructor argument list is a code smell that the type is violating the Single Responsibility Principle.
- Tests follow the seams DI creates. Real implementations of Stable Dependencies. Hand-written in-memory fakes for Volatile ones. Integration tests hit a real Postgres; do not mock what you own.
- The UDD client is a Volatile Dependency behind an interface owned by the ingest domain. Tests run against recorded fixtures, never against `udd-mirror.debian.net`.

### Layered architecture and abstraction ownership

- Split applications into layers. For a backend, typically view, domain, and data access.
- Each layer declares the abstractions it consumes. The interface belongs to the consumer, not to the implementer. This is idiomatic Go: interfaces are declared where they are used, and implementations satisfy them structurally.
- The domain declares the storage interfaces it needs. The data access layer imports and implements them. The domain does not depend on data access.
- Counter-pattern to reject: each layer depending on the next-lower one (view depending on domain depending on data access). That is naive layering, not Dependency Inversion.
- This is the Dependency Inversion Principle from SOLID, applied at the module boundary.
- Package layout follows the layering. `internal/` holds everything not meant for outside consumption, which is nearly all of it.

### Strict typing

- Make illegal states unrepresentable. Named types over bare primitives where a primitive would let two unrelated values be swapped: `PackageName`, `SuiteName`, `BugNumber`.
- No `any` or `interface{}` in domain code. It appears only where a library forces it, and is parsed into a domain type immediately.
- No unchecked type assertions. `x.(T)` without the comma-ok form is a lint failure. `forcetypeassert` is enabled.
- Parse, do not validate. A boundary constructor returns a domain type or an error; it never returns the raw shape plus a boolean. Once a value has a domain type, every consumer may trust it without re-checking.
- Parse at every process boundary: HTTP requests, environment variables, rows read from UDD, files. Types are derived from the parse, never asserted into existence.
- Errors are values and are always handled. `errcheck` is enabled with no ignore list. Wrap with `%w` and enough context to identify what was attempted with what inputs.
- `panic` is for programmer errors detected during startup, never for anything reachable by a request or a row of upstream data.
- Relaxing strictness is always a code smell and is fixed, not papered over. A `//nolint` directive requires a comment giving the reason and survives review only if that reason is a genuine constraint.

### Data and SQL

This is a database project. The schema is the design.

- Migrations are forward-only, numbered, checked in, and applied by a single command. No hand-edited production schema.
- Our schema is ours. UDD's shape is an input, not a contract: ingest reads UDD and writes our tables. Upstream renaming a column breaks one adapter, not the application.
- SQL is hand-written and lives in `.sql` files. `sqlc` generates typed Go from those files, so the query and its result type cannot drift apart. No ORM, no runtime query building, no reflection-based scanning.
- Every query the web tier runs has a bounded result set and an index that serves it. `EXPLAIN` before shipping a new query shape.
- Ingest never mutates rows the web tier is reading. Load into a staging table, then swap or upsert inside a transaction.
- Ingest is idempotent. Running it twice over the same upstream snapshot is a no-op, and it records which upstream snapshot produced the current data.

### Code comments

- Code is self-evident. Comments are a last resort.
- A comment is only justified by a non-obvious WHY: a hidden constraint, a subtle invariant, a workaround for a specific bug.
- Comments that restate WHAT the code does are noise.
- Doc comments on exported identifiers are a separate thing and follow the usual Go convention. They state contract, not implementation.

### Dependency hygiene

- Start with the standard library: `net/http`, `html/template`, `database/sql`, `log/slog`, `context`, `errors`, `testing`, `embed`, `flag`. The Go standard library covers most of what this project needs.
- Vet every dependency: can the standard library do it; size of transitive tree; active maintenance.
- Prefer governance-neutral dependencies over OSS that exists to feed a vendor's hosted SaaS. In that pattern the roadmap follows the vendor's revenue, not user need: the best features land in the paid tier, the self-hosting path degrades, telemetry and login prompts creep into the CLI, and at the extreme the licence is rewritten to forbid competing hosted offerings. The integration cost falls on us either way, whether we accept the drift or migrate to a fork. Use such dependencies only when no governance-neutral alternative exists; document the choice in the PR.
- If under 50 lines, write it. Do not depend.
- Vendor utilities under 100 lines with attribution and the original license.
- Prefer libraries with minimal transitive trees.
- Budget: under 10 direct runtime modules in `go.mod` across the whole repository. Build-time and test-only tools do not count against it but are still vetted. Exceeding requires written justification in the PR.
- Pin exactly. `go.mod` and `go.sum` are committed, `flake.lock` is committed, and no input resolves to a floating reference. Renovate proposes version changes; humans review.
- No npm and no `node_modules`, in development, in CI, or in an image. Front-end dependencies are either vendored as a single reviewed file or provided as a standalone binary by the flake. Any front-end tool that can only be installed from a package registry is out.
- Front-end assets are built at image-build time and embedded into the Go binary. Nothing is fetched from a CDN at runtime, so no third party sees our users.

### Operational discipline

The baselines below apply to every service. They are the floor, not the ceiling.

- Error handling and failure modes. No silent catches. Errors carry context (what was attempted, with what inputs, what failed). Distinguish recoverable errors from programmer errors. Boundaries decide what to surface to the user; deeper layers return errors and trust the boundary.
- Observability by default. Structured logs via `log/slog` in production. Consistent metric naming. Every sync run records what it fetched, how long it took, how many rows changed, and which upstream snapshot it read. If you cannot see it, you cannot fix it.
- `context.Context` is threaded through every IO path and carries the deadline. No unbounded waits.
- Security baseline. Secrets never in code, never in committed `.env` files. Least-privilege on every credential: the web tier connects to Postgres as a read-only role, ingest as the only writer. Dependency vulnerabilities handled under the rules above, with `govulncheck` in CI.
- Privacy. The site has no accounts and collects no personal data. No third-party scripts, no analytics that identify individuals, no cookies that are not strictly necessary. Server logs retain no full IP addresses. Keeping this true is a design constraint on every new feature.
- Everything we serve is derived from public Debian data under its original licences. Attribution and a link back to the authoritative Debian page belong on every page that reproduces upstream content.

### Local checks before pushing

- Run cheap checks locally: formatter, linter, type-check, tests that exercise the touched code.
- One command formats everything, including Go, Nix, Markdown, YAML, and shell: `nix fmt`. It is the fix variant by construction; `nix flake check` fails when anything is unformatted.
- One command runs the whole baseline: `nix flake check`. It covers formatting, linting, vulnerability scanning, and tests, over every git-checked file.
- Scope to what changed while iterating: inside the dev shell, `go test ./internal/ingest/...` and `golangci-lint run ./internal/ingest/...` behave normally.

### Git conventions

- [Conventional Commits](https://www.conventionalcommits.org/) for every commit message. Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`, `revert`. Scope optional and lowercase, e.g. `feat(ingest):`, `fix(web):`.
- Imperative mood. "Add the X", not "Added the X".
- Subject line under 72 characters. Body wraps at 80.
- Body explains why when the why is not obvious from the diff. Reference the relevant GitHub issue or decision record.
- No value-judgement words: "improve", "better", "cleaner", "robust", "elegant". State the change in concrete terms.
- No vague qualifiers: "various", "several minor", "ensure", "make sure".
- No AI attribution and no Co-Authored-By for AI tools.
- One commit per PR is the default. Squash on merge. Split larger changes at natural seams.

### Decision records

- Choices that are expensive to reverse are written down in `engineering/decisions/` as numbered records: context, decision, consequences.
- A decision record is superseded, never edited into a lie. Add a new record and link it from the old one.
- Anything touching how we read Debian data gets a record, because those choices bind us to somebody else's infrastructure.

### Continuous integration

- Every PR runs the same baseline: format check, lint, tests, vulnerability scan. Identical commands locally and in CI, because both run `nix flake check`.
- One workflow at `.github/workflows/ci.yml`. It installs Nix and runs the flake checks. CI holds no tool versions of its own.
- CI never connects to `udd-mirror.debian.net`. Integration tests run against a Postgres container seeded from checked-in fixtures.

### Reproducible, one-command developer environments

- Same language, runtime, and tool versions across every developer machine and CI. `flake.nix` and `flake.lock` are the single source of truth for every system dependency.
- Nothing is installed on the host and nothing is assumed present on it. If a tool is not in the flake, it does not exist as far as this project is concerned. A build or boot that depends on something outside the flake is a bug.
- `.envrc` containing `use flake` is recommended and git-ignored, so direnv users get the shell automatically and everyone else runs `nix develop`.
- One command to start: `nix run .#dev-up`. One command to destroy: `nix run .#dev-down`, which removes containers, volumes, network, and generated state.
- The development environment leaves no trace on the host. Services run in a rootless podman pod on a private network, published only on the loopback interface, with state in named volumes.
- Development dependencies are production dependencies. The dev environment runs the same OCI images Nix builds for deployment, from the same derivations.

### Toolchain choices

- [Nix](https://nixos.org/) flakes for every system dependency, the development shell, the OCI images, and the CI environment. One lock file, one closure, no second version manager.
- Go for the ingest job and the web server. Static binaries, a standard library that covers most of the project, and an OCI image that contains the binary and CA certificates and little else.
- Container images built with `pkgs.dockerTools.buildLayeredImage`, not `Dockerfile`s. The image contents are derived from the same derivations the dev shell uses, so there is no second dependency specification to drift.
- Orchestration is a `docker-compose.yaml` generated from Nix and run under rootless podman. Generating it is a small expression, which is cheaper than taking on an orchestration dependency.
- Postgres for storage, matching the major version UDD itself runs where practical. Full-text search and trigram indexes come from Postgres; no separate search engine until Postgres demonstrably cannot do the job.
- [pgx](https://github.com/jackc/pgx) as the Postgres driver and [sqlc](https://sqlc.dev/) to generate typed Go from hand-written SQL at build time.
- Server-rendered HTML from `html/template`, embedded with `embed`. No single-page-application framework. The server sends HTML, never JSON destined for a client-side renderer.
- [htmx](https://htmx.org/) for interactivity, vendored as a single reviewed file and served from `embed`. Responses to htmx requests are HTML fragments rendered by the same templates as the full page. Every htmx interaction is layered over a form or link that already works with JavaScript disabled: `hx-get` on a form that also has a real `action`, never a `div` with a click handler.
- [Tailwind CSS](https://tailwindcss.com/) for styling, compiled by the standalone `tailwindcss` binary the flake provides. It runs at build time only, scans the templates, and emits one stylesheet that is embedded into the Go binary. No npm, no PostCSS pipeline, no runtime CSS.
- Testing: the standard `testing` package. Integration tests run against a real Postgres from the dev environment.
- [golangci-lint](https://golangci-lint.run/) for linting and [treefmt](https://treefmt.com/) for formatting every file type in one pass.
- Dependency updates via [Renovate](https://docs.renovatebot.com/) (hosted [Mend Renovate Bot](https://developer.mend.io/), free; Dependabot lacks `minimumReleaseAge`). Weekly batch, 7 day minimum release age, no auto-merge. Security advisories handled same-day.

### Writing style

- Sentence-case in headings.
- Bold sparingly.
- No em-dashes. Use a colon, comma, semicolon, period, or rephrase.
- No rhetorical triplets ("one X, one Y, one Z" and similar three-beat patterns).
- No AI attribution in commit messages or code.

---

## Use of AI and LLMs

This project accepts the use of LLMs to write code. The tools used to build software are changing quickly, but this code is held to the same standards as code written without LLMs.

- Tests at the unit, integration, and end-to-end levels.
- Strict typing and parsed boundaries.
- Dependency hygiene and supply-chain discipline as set out above.
- Architecture and code review applied to every change, AI-assisted or not.

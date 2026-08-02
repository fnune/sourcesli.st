# Requirements

What the project has to do, and the constraints it has to do it under. Principles and conventions live in [engineering/README.md](engineering/README.md); this file is the specification.

## Product

### Package browsing

- Search binary and source packages across suites, with autocompletion served from our own database.
- A package page shows versions per suite, dependencies, the maintainer, a description, and links back to the authoritative Debian pages.
- Bugs for a package appear on the package page, filterable by severity and status, without a second lookup elsewhere.
- Every page is reachable and readable with JavaScript disabled.

### Bug browsing

- Search and filter bugs by package, severity, status, and tag.
- A bug page shows the metadata UDD carries and links to the BTS for the full log.

### ISO chooser

- Answer the question "which Debian image do I actually want" without requiring prior knowledge of Debian's release and media structure.
- Cover architecture, installer type, desktop environment, and whether non-free firmware is needed.
- Produce a direct download link, plus the matching checksum and signature so verification is possible.
- This is the part of the product with no good existing alternative, so it earns the most attention.

### Non-goals

- No accounts, no user-generated content, no comments.
- Not a replacement for `tracker.debian.org`, `sources.debian.org`, or the BTS. We link to them rather than reimplement them.
- No source-code browsing or code search. `sources.debian.org` and `codesearch.debian.net` already do this well.
- No package downloads and no archive mirroring. We serve metadata.
- No Ubuntu or other derivatives, even though UDD carries some of that data.

## Upstream conduct

Hard requirements, not preferences. Expanded in the Debian data citizenship section of the engineering principles and in [decision record 0001](engineering/decisions/0001-source-bulk-data-from-udd.md).

- A user request must never cause a request to a Debian service.
- Bulk data comes from the published UDD dump, fetched conditionally. No scraping of Debian web interfaces, ever.
- No automated job queries `udd-mirror.debian.net`. It is a shared database, and our load there would be somebody else's problem.
- Sync cadence never exceeds upstream publication cadence.
- Every connection identifies the project and a contact address.
- Serving stale data is preferred to additional upstream load, and staleness is shown to the user.
- Development, tests, and CI run fully offline against fixtures.

## Build and run

- One command builds everything that gets deployed.
- One command sets up a working development environment.
- One command destroys it, leaving no trace on the host: no installed packages, no state outside named volumes, nothing published beyond the loopback interface.
- Development dependencies are production dependencies, from the same derivations.
- Every system dependency comes from the Nix flake. A build or boot that depends on something outside the flake is a bug.
- An `.envrc` containing `use flake` is recommended and git-ignored.

## Static checks

- One command lints the whole project. One command formats it.
- Both cover every git-checked file, not only Go: Markdown, Nix, YAML, and shell included.
- Every checking command has a fix variant.
- Local and CI invocations are the same command.

## Software

- Strict typing throughout. No unchecked type assertions, no `any` in domain code, no dropped errors.
- Parse, do not validate: boundaries return domain types or errors.
- Dependency injection is what makes the code testable, and tests are written alongside the code rather than after it.
- Comments explain why, never what. Code is self-evident or it gets rewritten.
- Technology choices are boring and stable. Nix, htmx, and Tailwind are the deliberate exceptions, each recorded with its reasoning.
- Deployment is deferred. The build produces OCI images that run anywhere; choosing a host and writing the infrastructure code is a separate decision, not a gap in the application.

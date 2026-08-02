# 0002. Nix as the only source of system dependencies

Status: accepted

## Context

The project needs the same toolchain on every developer machine, in CI, and inside the images it deploys. The usual arrangement layers several mechanisms: a version manager for language runtimes, `Dockerfile`s that install packages again from a distribution's repositories, and a CI configuration that pins yet another set. Three specifications of the same thing, drifting independently.

Go is a static-binary language, which makes the runtime image nearly trivial if the build system can produce one. Nix can, through `dockerTools`, from the same derivations that back the development shell.

## Decision

`flake.nix` and `flake.lock` are the single source of truth for every system dependency: the Go toolchain, `sqlc`, `golangci-lint`, `treefmt`, `tailwindcss`, the Postgres client, and the container tooling.

If a tool is not in the flake, it does not exist as far as this project is concerned. A build or boot that depends on something present on the host is a bug, not a convenience.

No `Dockerfile`s. Images are built with `pkgs.dockerTools.buildLayeredImage` from the same derivations the dev shell uses.

Orchestration is a `docker-compose.yaml` generated from a Nix expression and run under rootless podman. Generating it is small enough to own outright, which is cheaper than depending on an orchestration layer.

`.envrc` containing `use flake` is recommended and git-ignored. direnv users get the shell automatically; everyone else runs `nix develop`.

CI installs Nix and runs `nix flake check`. It holds no tool versions of its own.

## Consequences

There is exactly one thing to update when a version changes, and one lock file that records what was actually used.

Development dependencies are production dependencies, in the strong sense: the dev environment runs the images that get deployed, from the same derivations.

The runtime image contains the Go binary and CA certificates. No package manager, no shell, no distribution userland, so most CVE noise disappears along with it.

Nix becomes a hard prerequisite for contributors, and its learning curve is real. Accepted deliberately: the alternative is the three-specification drift described above.

Front-end tooling is constrained by this. Anything that can only be installed from npm is excluded, because that would reintroduce a second dependency specification. htmx is vendored as a single reviewed file; Tailwind is consumed as the standalone binary nixpkgs provides, at build time only.

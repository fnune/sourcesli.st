# Every recipe assumes the dev shell: `nix develop`, or direnv.

default:
    @just --list

# Build the images and start the development environment.
up:
    nix run .#dev-up

# Destroy the development environment, volumes and images included.
down:
    nix run .#dev-down

# Run one ETL pass against the published UDD dump. Downloads 2.4 GiB.
ingest *args:
    nix run .#ingest -- {{ args }}

# Format everything: Go, Nix, shell, YAML, TOML, SQL, this justfile.
fmt:
    nix fmt

# Format check, lint, and tests. Exactly what CI runs.
check:
    nix flake check --print-build-logs

# Unit and handler tests. The ETL tests skip without TEST_DATABASE_URL.
test *args:
    go test ./... -count=1 {{ args }}

lint:
    golangci-lint run ./...

# Reads its database over the network, so it cannot live in `nix flake check`.
vuln:
    govulncheck ./...

# Regenerate committed tool output. CI fails on any diff this produces.
generate:
    sqlc generate
    tailwindcss --input web/styles/app.css --output web/assets/app.css --minify

images:
    nix build .#images-web --print-build-logs
    nix build .#images-ingest --print-build-logs

# Run the web server against a database you already have.
web:
    go run ./cmd/web

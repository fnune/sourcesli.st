set positional-arguments

database_url := "postgres://sourceslist:devonly@127.0.0.1:15432/sourceslist"
# A database of its own: the ETL tests drop and recreate the public schema.
test_database_url := "postgres://sourceslist:devonly@127.0.0.1:15432/sourceslist_test"
snapshots := ".dev/snapshots"
serve := "DATABASE_URL='" + database_url + "' LISTEN_ADDRESS=127.0.0.1:8080 go run ./cmd/web"
# One definition, so watching cannot write a stylesheet that differs from CI's.
build_stylesheet := "tailwindcss --input web/styles/app.css --output web/assets/app.css --minify"

default:
    @just --list

# Database, schema, and the site on http://127.0.0.1:8080, rebuilt as you edit. Start here.
[group('run')]
dev: up
    #!/usr/bin/env bash
    set -euo pipefail
    {{ build_stylesheet }} --watch &
    restyling=$!
    trap 'kill "$restyling" 2>/dev/null || true' EXIT
    watchexec --restart --wrap-process=session --debounce 500ms \
        --exts go,html,css --ignore 'web/styles/**' \
        --watch cmd --watch internal --watch web \
        -- {{ serve }}

# Start the database and bring the schema up to date, without serving. Safe to rerun.
[group('run')]
up: && migrate
    nix run .#dev-up

# Serve from source against a database that is already up. Ctrl-c, edit, rerun.
[group('run')]
web:
    {{ serve }}

# Apply migrations and seed data. No network, no dump, seconds.
[group('run')]
migrate:
    DATABASE_URL='{{ database_url }}' go run ./cmd/migrate

[group('run')]
psql *args:
    psql '{{ database_url }}' "$@"

# Stop the containers, keeping the database.
[group('run')]
stop:
    nix run .#dev-stop

# Destroy containers, volumes, and images. Prompts unless given --yes.
[group('run')]
down *args:
    nix run .#dev-down -- "$@"

# Replace the database with a snapshot: the newest, or one named as an argument.
[group('data')]
restore *file:
    #!/usr/bin/env bash
    set -euo pipefail
    dump="${1:-$(ls -t {{ snapshots }}/*.dump 2>/dev/null | head -1)}"
    if [ -z "$dump" ]; then
        echo "no snapshot in {{ snapshots }}. take one with 'just snapshot'." >&2
        exit 1
    fi
    echo "restoring $dump, replacing everything in the database"
    pg_restore --dbname '{{ database_url }}' --clean --if-exists --no-owner --jobs 4 "$dump"

# Write the database to a new timestamped file, so a teardown costs nothing.
[group('data')]
snapshot:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{ snapshots }}
    dump="{{ snapshots }}/$(date -u +%Y%m%dT%H%M%SZ).dump"
    pg_dump --dbname '{{ database_url }}' --format custom --compress 6 --file "$dump"
    ls -lh "$dump"

# One real ETL pass. The first downloads 2.4 GiB to .dev/cache; later runs reuse it.
[group('data')]
ingest *args:
    nix run .#ingest -- "$@"

# Build the images and run the web tier in a container too, on port 18080.
[group('images')]
stack:
    nix run .#dev-stack

[group('images')]
images:
    nix build .#images-web --print-build-logs
    nix build .#images-ingest --print-build-logs

# Format everything: Go, Nix, shell, YAML, TOML, SQL, this justfile.
[group('check')]
fmt:
    nix fmt

# Format check, lint, and tests. Exactly what CI runs.
[group('check')]
check:
    nix flake check --print-build-logs

# Unit and handler tests. The ETL tests need a database; see test-all.
[group('check')]
test *args:
    go test ./... -count=1 "$@"

# Every test, the ETL ones included, against a throwaway database.
[group('check')]
test-all *args:
    @psql '{{ database_url }}' -tAc "select 1 from pg_database where datname = 'sourceslist_test'" | grep -q 1 || createdb --maintenance-db '{{ database_url }}' sourceslist_test
    TEST_DATABASE_URL='{{ test_database_url }}' go test ./... -count=1 "$@"

[group('check')]
lint:
    golangci-lint run ./...

[group('check')]
vuln:
    govulncheck ./...

# Regenerate committed tool output. CI fails on any diff this produces.
[group('check')]
generate:
    sqlc generate
    {{ build_stylesheet }}

#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ENV_FILE can select .env.mysql; preserve the existing development default.
ENV_FILE="${ENV_FILE:-$PROJECT_ROOT/.env}"
if [ -f "$ENV_FILE" ]; then
    set -a
    source "$ENV_FILE"
    set +a
fi

DB_DRIVER="${DB_DRIVER:-postgres}"
case "$DB_DRIVER" in
    postgres) MIGRATIONS_DIR="${MIGRATIONS_DIR:-$PROJECT_ROOT/migrations/versioned}" ;;
    mysql) MIGRATIONS_DIR="${MIGRATIONS_DIR:-$PROJECT_ROOT/migrations/mysql}" ;;
    *) echo "Error: migrate.sh supports DB_DRIVER=postgres or mysql" >&2; exit 1 ;;
esac

if ! command -v migrate >/dev/null 2>&1; then
    echo "Error: migrate is required; install with: go install -tags 'postgres mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1" >&2
    exit 1
fi

command="${1:-}"
case "$command" in
    create)
        [ -n "${2:-}" ] || { echo "Usage: $0 create <migration_name>" >&2; exit 1; }
        migrate create -ext sql -dir "$MIGRATIONS_DIR" -seq "$2"
        exit 0
        ;;
    up|down|version) ;;
    force|goto)
        [ -n "${2:-}" ] || { echo "Usage: $0 $command <version>" >&2; exit 1; }
        ;;
    *) echo "Usage: $0 {up|down|create <name>|version|force <version>|goto <version>}" >&2; exit 1 ;;
esac

if [ -z "${DB_URL:-}" ]; then
    export DB_DRIVER
    # Shared native driver configuration avoids shell/Python interpolation of passwords.
    if command -v database-dsn >/dev/null 2>&1; then
        DB_URL="$(database-dsn)"
    elif command -v go >/dev/null 2>&1; then
        DB_URL="$(cd "$PROJECT_ROOT" && go run ./cmd/database-dsn)"
    else
        echo "Error: database-dsn is required; build with: go build -o database-dsn ./cmd/database-dsn" >&2
        exit 1
    fi
elif [ "$DB_DRIVER" = postgres ]; then
    # Preserve the existing local PostgreSQL DB_URL behavior.
    if [[ "$DB_URL" != *"sslmode="* ]]; then
        if [[ "$DB_URL" == *"?"* ]]; then DB_URL="${DB_URL}&sslmode=disable"; else DB_URL="${DB_URL}?sslmode=disable"; fi
    else
        DB_URL="${DB_URL//sslmode=require/sslmode=disable}"
        DB_URL="${DB_URL//sslmode=prefer/sslmode=disable}"
    fi
fi

# Never echo the DSN or credentials. Dirty MySQL databases require a reviewed
# repair; force remains an explicit operator action, never automatic recovery.
echo "Running $DB_DRIVER migration command: $command"
case "$command" in
    force) migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" force -- "$2" ;;
    goto) migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" goto "$2" ;;
    *) migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" "$command" ;;
esac
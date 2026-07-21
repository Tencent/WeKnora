#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if [ -f "$PROJECT_ROOT/.env" ]; then
    echo "Loading .env file from $PROJECT_ROOT/.env"
    set -a
    # shellcheck source=/dev/null
    source "$PROJECT_ROOT/.env"
    set +a
fi

DB_DRIVER="$(printf '%s' "${DB_DRIVER:-postgres}" | tr '[:upper:]' '[:lower:]')"
DB_HOST="${DB_HOST:-localhost}"
DB_USER="${DB_USER:-postgres}"
DB_PASSWORD="${DB_PASSWORD:-postgres}"
DB_NAME="${DB_NAME:-WeKnora}"

case "$DB_DRIVER" in
    postgres)
        DB_PORT="${DB_PORT:-5432}"
        DEFAULT_MIGRATIONS_DIR="migrations/versioned"
        MIGRATE_BUILD_TAGS="postgres"
        ;;
    mysql)
        DB_PORT="${DB_PORT:-3306}"
        DEFAULT_MIGRATIONS_DIR="migrations/mysql"
        MIGRATE_BUILD_TAGS="mysql"
        ;;
    *)
        echo "Error: scripts/migrate.sh supports DB_DRIVER=postgres or mysql, got '$DB_DRIVER'" >&2
        exit 1
        ;;
esac

MIGRATIONS_DIR="${MIGRATIONS_DIR:-$DEFAULT_MIGRATIONS_DIR}"
if [[ "$MIGRATIONS_DIR" != /* ]]; then
    MIGRATIONS_DIR="$PROJECT_ROOT/$MIGRATIONS_DIR"
fi

if ! command -v migrate >/dev/null 2>&1; then
    echo "Error: migrate tool is not installed" >&2
    echo "Install it with:" >&2
    echo "  go install -tags '$MIGRATE_BUILD_TAGS' github.com/golang-migrate/migrate/v4/cmd/migrate@latest" >&2
    echo "To manage both PostgreSQL and MySQL, install with -tags 'postgres mysql'." >&2
    exit 1
fi

url_encode() {
    local value="$1"
    if command -v python3 >/dev/null 2>&1; then
        python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$value"
    elif command -v python >/dev/null 2>&1; then
        python -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$value"
    else
        echo "Error: python3 or python is required to safely encode database credentials" >&2
        return 1
    fi
}

if [ -z "${DB_URL:-}" ]; then
    ENCODED_USER="$(url_encode "$DB_USER")"
    ENCODED_PASSWORD="$(url_encode "$DB_PASSWORD")"
    ENCODED_NAME="$(url_encode "$DB_NAME")"

    if [ "$DB_DRIVER" = "mysql" ]; then
        DB_URL="mysql://${ENCODED_USER}:${ENCODED_PASSWORD}@tcp(${DB_HOST}:${DB_PORT})/${ENCODED_NAME}?multiStatements=true&parseTime=true&loc=UTC"
    else
        DB_SSLMODE="${DB_SSLMODE:-disable}"
        DB_URL="postgres://${ENCODED_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${ENCODED_NAME}?sslmode=${DB_SSLMODE}"
    fi
fi

run_migrate() {
    migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" "$@"
}

print_target() {
    echo "Driver: $DB_DRIVER"
    echo "Database: $DB_USER@$DB_HOST:$DB_PORT/$DB_NAME"
    echo "Migrations: $MIGRATIONS_DIR"
}

case "${1:-}" in
    up)
        echo "Running migrations up..."
        print_target
        run_migrate up
        ;;
    down)
        echo "Running migrations down..."
        print_target
        run_migrate down
        ;;
    create)
        if [ -z "${2:-}" ]; then
            echo "Error: migration name is required" >&2
            echo "Usage: $0 create <migration_name>" >&2
            exit 1
        fi
        echo "Creating migration files for $2 in $MIGRATIONS_DIR..."
        migrate create -ext sql -dir "$MIGRATIONS_DIR" -seq "$2"
        ;;
    version)
        echo "Checking current migration version..."
        print_target
        run_migrate version
        ;;
    force)
        if [ -z "${2:-}" ]; then
            echo "Error: version number is required" >&2
            echo "Usage: $0 force <version>" >&2
            exit 1
        fi
        echo "Forcing migration version to $2..."
        env migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" force -- "$2"
        ;;
    goto)
        if [ -z "${2:-}" ]; then
            echo "Error: version number is required" >&2
            echo "Usage: $0 goto <version>" >&2
            exit 1
        fi
        echo "Migrating to version $2..."
        run_migrate goto "$2"
        ;;
    *)
        echo "Usage: $0 {up|down|create <migration_name>|version|force <version>|goto <version>}" >&2
        exit 1
        ;;
esac

echo "Migration command completed successfully"

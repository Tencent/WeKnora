#!/bin/bash
set -e

# Get the script directory and project root
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Load .env file if it exists (for development mode)
if [ -f "$PROJECT_ROOT/.env" ]; then
    echo "Loading .env file from $PROJECT_ROOT/.env"
    set -a
    source "$PROJECT_ROOT/.env"
    set +a
fi
cd "$PROJECT_ROOT"

# Database connection details (can be overridden by environment variables)
DB_DRIVER=$(printf '%s' "${DB_DRIVER:-postgres}" | tr '[:upper:]' '[:lower:]')
DB_HOST=${DB_HOST:-localhost}
case "$DB_DRIVER" in
    mysql)
        DB_PORT=${DB_PORT:-3306}
        DB_USER=${DB_USER:-root}
        DB_PASSWORD=${DB_PASSWORD:-}
        DEFAULT_MIGRATIONS_DIR="migrations/mysql"
        ;;
    postgres)
        DB_PORT=${DB_PORT:-5432}
        DB_USER=${DB_USER:-postgres}
        DB_PASSWORD=${DB_PASSWORD:-postgres}
        DEFAULT_MIGRATIONS_DIR="migrations/versioned"
        ;;
    *)
        echo "Error: unsupported DB_DRIVER '$DB_DRIVER' (expected postgres or mysql)"
        exit 1
        ;;
esac
DB_NAME=${DB_NAME:-WeKnora}

# Use the migration stream for the selected business database.
MIGRATIONS_DIR="${MIGRATIONS_DIR:-$DEFAULT_MIGRATIONS_DIR}"

# Check if migrate tool is installed
if ! command -v migrate &> /dev/null; then
    echo "Error: migrate tool is not installed"
    echo "Install it with: go install -tags 'postgres mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"
    exit 1
fi

# Construct the database URL. DB_URL can override this for TLS or managed
# database parameters that are specific to a deployment.
if [ -n "$DB_URL" ]; then
    # Keep the historical local-Postgres sslmode behavior. MySQL URLs use
    # go-sql-driver query parameters and must not receive a PostgreSQL option.
    if [ "$DB_DRIVER" = "postgres" ] && [[ "$DB_URL" != *"sslmode="* ]]; then
        # Add sslmode=disable if not present
        if [[ "$DB_URL" == *"?"* ]]; then
            DB_URL="${DB_URL}&sslmode=disable"
        else
            DB_URL="${DB_URL}?sslmode=disable"
        fi
    elif [ "$DB_DRIVER" = "postgres" ] && { [[ "$DB_URL" == *"sslmode=require"* ]] || [[ "$DB_URL" == *"sslmode=prefer"* ]]; }; then
        # Replace sslmode=require/prefer with sslmode=disable for local dev
        DB_URL="${DB_URL//sslmode=require/sslmode=disable}"
        DB_URL="${DB_URL//sslmode=prefer/sslmode=disable}"
    fi
else
    if [ "$DB_DRIVER" = "mysql" ]; then
        # golang-migrate's MySQL backend accepts the native go-sql-driver DSN
        # after the mysql:// prefix. Quoting DB_URL at every invocation keeps
        # shell metacharacters in credentials inert.
        DB_URL="mysql://${DB_USER}:${DB_PASSWORD}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?multiStatements=true&parseTime=true"
    else
        # URL-encode credentials without interpolating secrets into source code.
        if command -v python3 &> /dev/null; then
            ENCODED_USER=$(DB_VALUE="$DB_USER" python3 -c 'import os, urllib.parse; print(urllib.parse.quote(os.environ["DB_VALUE"], safe=""))')
            ENCODED_PASSWORD=$(DB_VALUE="$DB_PASSWORD" python3 -c 'import os, urllib.parse; print(urllib.parse.quote(os.environ["DB_VALUE"], safe=""))')
        else
            ENCODED_USER="$DB_USER"
            ENCODED_PASSWORD="$DB_PASSWORD"
        fi
        DB_URL="postgres://${ENCODED_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
    fi
fi

# Execute migration based on command
case "$1" in
    up)
        echo "Running migrations up..."
        echo "DB_DRIVER: ${DB_DRIVER}"
        echo "DB_USER: ${DB_USER}"
        echo "DB_HOST: ${DB_HOST}"
        echo "DB_PORT: ${DB_PORT}"
        echo "DB_NAME: ${DB_NAME}"
        echo "MIGRATIONS_DIR: ${MIGRATIONS_DIR}"
        migrate -path "${MIGRATIONS_DIR}" -database "${DB_URL}" up
        ;;
    down)
        echo "Running migrations down..."
        migrate -path "${MIGRATIONS_DIR}" -database "${DB_URL}" down
        ;;
    create)
        if [ -z "$2" ]; then
            echo "Error: Migration name is required"
            echo "Usage: $0 create <migration_name>"
            exit 1
        fi
        echo "Creating migration files for $2..."
        migrate create -ext sql -dir "${MIGRATIONS_DIR}" -seq "$2"
        echo "Created:"
        echo "  - ${MIGRATIONS_DIR}/$(ls -t ${MIGRATIONS_DIR} | head -1)"
        echo "  - ${MIGRATIONS_DIR}/$(ls -t ${MIGRATIONS_DIR} | head -2 | tail -1)"
        ;;
    version)
        echo "Checking current migration version..."
        migrate -path "${MIGRATIONS_DIR}" -database "${DB_URL}" version
        ;;
    force)
        if [ -z "$2" ]; then
            echo "Error: Version number is required"
            echo "Usage: $0 force <version>"
            echo "Note: Use -1 to reset to no version (allows re-running all migrations)"
            exit 1
        fi
        VERSION="$2"
        echo "Forcing migration version to $VERSION..."
        # Use env to pass the command, avoiding shell flag parsing issues with negative numbers
        env migrate -path "${MIGRATIONS_DIR}" -database "${DB_URL}" force -- "$VERSION"
        ;;
    goto)
        if [ -z "$2" ]; then
            echo "Error: Version number is required"
            echo "Usage: $0 goto <version>"
            exit 1
        fi
        echo "Migrating to version $2..."
        migrate -path "${MIGRATIONS_DIR}" -database "${DB_URL}" goto "$2"
        ;;
    *)
        echo "Usage: $0 {up|down|create <migration_name>|version|force <version>|goto <version>}"
        exit 1
        ;;
esac

echo "Migration command completed successfully"

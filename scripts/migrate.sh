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

# Database driver detection (postgres | mysql). Default to postgres.
DB_DRIVER="${DB_DRIVER:-postgres}"

# Per-driver defaults: port, URL scheme, migration dir, go build tag.
case "$DB_DRIVER" in
    postgres)
        DB_PORT_DEFAULT=5432
        DB_SCHEME="postgres://"
        MIGRATIONS_DIR_DEFAULT="migrations/versioned"
        BUILD_TAGS="postgres"
        ;;
    mysql)
        DB_PORT_DEFAULT=3306
        DB_SCHEME="mysql://"
        MIGRATIONS_DIR_DEFAULT="migrations/mysql"
        BUILD_TAGS="mysql"
        ;;
    *)
        echo "Error: unsupported DB_DRIVER='${DB_DRIVER}' (expected 'postgres' or 'mysql')"
        exit 1
        ;;
esac

# Database connection details (can be overridden by environment variables)
DB_HOST=${DB_HOST:-localhost}
DB_PORT=${DB_PORT:-$DB_PORT_DEFAULT}
DB_USER=${DB_USER:-postgres}
DB_PASSWORD=${DB_PASSWORD:-postgres}
DB_NAME=${DB_NAME:-WeKnora}

# Migrations directory (defaults are per-driver, set above)
MIGRATIONS_DIR="${MIGRATIONS_DIR:-$MIGRATIONS_DIR_DEFAULT}"

# Check if migrate tool is installed
if ! command -v migrate &> /dev/null; then
    echo "Error: migrate tool is not installed"
    echo "Install it with: go install -tags '${BUILD_TAGS}' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"
    exit 1
fi

# Construct the database URL.
# If DB_URL is already set in .env, use it directly (postgres only gets sslmode
# normalization). Otherwise build it from individual components.
#
# SECURITY: DB_PASSWORD is never passed via a CLI argument (which would leak it
# through the process list / `ps`). For URL-encoding we pass it to python3
# via a subshell environment so it is not exported globally.

if [ -n "$DB_URL" ]; then
    if [ "$DB_DRIVER" = "postgres" ]; then
        # Ensure sslmode=disable is set for local dev (unless an sslmode is
        # already specified).
        if [[ "$DB_URL" != *"sslmode="* ]]; then
            if [[ "$DB_URL" == *"?"* ]]; then
                DB_URL="${DB_URL}&sslmode=disable"
            else
                DB_URL="${DB_URL}?sslmode=disable"
            fi
        elif [[ "$DB_URL" == *"sslmode=require"* ]] || [[ "$DB_URL" == *"sslmode=prefer"* ]]; then
            # Replace sslmode=require/prefer with sslmode=disable for local dev
            DB_URL="${DB_URL//sslmode=require/sslmode=disable}"
            DB_URL="${DB_URL//sslmode=prefer/sslmode=disable}"
        fi
    fi
    # For mysql we trust DB_URL as-is.
else
    # URL-encode the password via python3, reading it from the environment
    # (NOT from a -c argument) so it never appears in the process list.
    if command -v python3 &> /dev/null; then
        ENCODED_PASSWORD=$(DB_PASSWORD="$DB_PASSWORD" python3 -c "import os,urllib.parse; print(urllib.parse.quote(os.environ['DB_PASSWORD'], safe=''))")
    else
        # Fallback: no encoding (may break if the password has special chars)
        ENCODED_PASSWORD="$DB_PASSWORD"
    fi

    if [ "$DB_DRIVER" = "postgres" ]; then
        DB_URL="${DB_SCHEME}${DB_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
    else
        # MySQL DSN params:
        #   charset / collation : utf8mb4 + utf8mb4_0900_ai_ci
        #   parseTime=true       : convert DATE/DATETIME to time.Time
        #   loc=UTC              : interpret times as UTC
        #   multiStatements=true : required by golang-migrate to run a whole
        #                          migration file (multiple statements) in one call
        # MySQL tcp() address for IPv6 must be bracketed: tcp([::1]:3306)
        if echo "$DB_HOST" | grep -q ':'; then
            MYSQL_ADDR="[${DB_HOST}]:${DB_PORT}"
        else
            MYSQL_ADDR="${DB_HOST}:${DB_PORT}"
        fi
        DB_URL="${DB_SCHEME}${DB_USER}:${ENCODED_PASSWORD}@tcp(${MYSQL_ADDR})/${DB_NAME}?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true&loc=UTC&multiStatements=true"
    fi
fi

# Execute migration based on command
case "$1" in
    up)
        echo "Running migrations up..."
        echo "DB_DRIVER: ${DB_DRIVER}"
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
        echo "  - ${MIGRATIONS_DIR}/$(ls -t "${MIGRATIONS_DIR}" | head -1)"
        echo "  - ${MIGRATIONS_DIR}/$(ls -t "${MIGRATIONS_DIR}" | head -2 | tail -1)"
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

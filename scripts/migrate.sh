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

# Database connection details (can be overridden by environment variables)
DB_DRIVER=${DB_DRIVER:-postgres}
DB_HOST=${DB_HOST:-localhost}
if [ -z "${DB_PORT:-}" ]; then
    case "$DB_DRIVER" in
        mysql) DB_PORT=3306 ;;
        *) DB_PORT=5432 ;;
    esac
fi
DB_USER=${DB_USER:-postgres}
DB_PASSWORD=${DB_PASSWORD-postgres}
DB_NAME=${DB_NAME:-WeKnora}

# Use driver-specific migrations directory
case "$DB_DRIVER" in
    postgres) DEFAULT_MIGRATIONS_DIR="migrations/versioned" ;;
    mysql) DEFAULT_MIGRATIONS_DIR="migrations/mysql" ;;
    *) echo "Error: unsupported DB_DRIVER '$DB_DRIVER' (expected postgres or mysql)" && exit 1 ;;
esac
MIGRATIONS_DIR="${MIGRATIONS_DIR:-$DEFAULT_MIGRATIONS_DIR}"

# Check if migrate tool is installed
if ! command -v migrate &> /dev/null; then
    echo "Error: migrate tool is not installed"
    echo "Install it with: go install -tags 'postgres mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"
    exit 1
fi

# Construct the database URL
# If DB_URL is already set in .env, use it. For postgres, ensure sslmode=disable is set
# Otherwise, construct it from individual components
if [ -n "$DB_URL" ]; then
    if [ "$DB_DRIVER" = "postgres" ]; then
        if [[ "$DB_URL" != *"sslmode="* ]]; then
            if [[ "$DB_URL" == *"?"* ]]; then
                DB_URL="${DB_URL}&sslmode=disable"
            else
                DB_URL="${DB_URL}?sslmode=disable"
            fi
        elif [[ "$DB_URL" == *"sslmode=require"* ]] || [[ "$DB_URL" == *"sslmode=prefer"* ]]; then
            DB_URL="${DB_URL//sslmode=require/sslmode=disable}"
            DB_URL="${DB_URL//sslmode=prefer/sslmode=disable}"
        fi
    fi
else
    if command -v python3 &> /dev/null; then
        ENCODED_PASSWORD=$(python3 -c 'import os, urllib.parse; print(urllib.parse.quote(os.environ.get("DB_PASSWORD", ""), safe=""))')
    else
        ENCODED_PASSWORD="$DB_PASSWORD"
    fi
    case "$DB_DRIVER" in
        postgres)
            DB_URL="postgres://${DB_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
            ;;
        mysql)
            DB_URL="mysql://${DB_USER}:${ENCODED_PASSWORD}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true"
            ;;
    esac
fi

# Execute migration based on command
case "$1" in
    up)
        echo "Running migrations up..."
        echo "DB_URL: <redacted>"
        echo "DB_DRIVER: ${DB_DRIVER}"
        echo "DB_USER: ${DB_USER}"
        if [ -n "$DB_PASSWORD" ]; then
            echo "DB_PASSWORD: ********"
        else
            echo "DB_PASSWORD: <empty>"
        fi
        echo "DB_HOST: ${DB_HOST}"
        echo "DB_PORT: ${DB_PORT}"
        echo "DB_NAME: ${DB_NAME}"
        echo "MIGRATIONS_DIR: ${MIGRATIONS_DIR}"
        migrate -path ${MIGRATIONS_DIR} -database ${DB_URL} up
        ;;
    down)
        echo "Running migrations down..."
        migrate -path ${MIGRATIONS_DIR} -database ${DB_URL} down
        ;;
    create)
        if [ -z "$2" ]; then
            echo "Error: Migration name is required"
            echo "Usage: $0 create <migration_name>"
            exit 1
        fi
        echo "Creating migration files for $2..."
        migrate create -ext sql -dir ${MIGRATIONS_DIR} -seq $2
        echo "Created:"
        echo "  - ${MIGRATIONS_DIR}/$(ls -t ${MIGRATIONS_DIR} | head -1)"
        echo "  - ${MIGRATIONS_DIR}/$(ls -t ${MIGRATIONS_DIR} | head -2 | tail -1)"
        ;;
    version)
        echo "Checking current migration version..."
        migrate -path ${MIGRATIONS_DIR} -database ${DB_URL} version
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
        migrate -path ${MIGRATIONS_DIR} -database ${DB_URL} goto $2
        ;;
    *)
        echo "Usage: $0 {up|down|create <migration_name>|version|force <version>|goto <version>}"
        exit 1
        ;;
esac

echo "Migration command completed successfully"

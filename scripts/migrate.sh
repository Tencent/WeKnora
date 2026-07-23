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

# Database driver detection
DB_DRIVER=${DB_DRIVER:-postgres}

# Database connection details (can be overridden by environment variables)
DB_HOST=${DB_HOST:-localhost}
DB_USER=${DB_USER:-postgres}
DB_PASSWORD=${DB_PASSWORD:-postgres}
DB_NAME=${DB_NAME:-WeKnora}

# Set driver-specific defaults
if [ "$DB_DRIVER" = "mysql" ]; then
    DB_PORT=${DB_PORT:-3306}
    MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations/mysql}"
else
    DB_PORT=${DB_PORT:-5432}
    MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations/versioned}"
fi

# Check if migrate tool is installed
if ! command -v migrate &> /dev/null; then
    echo "Error: migrate tool is not installed"
    echo "Install it with: go install -tags 'postgres mysql sqlite3' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"
    exit 1
fi

# Construct the database URL
if [ "$DB_DRIVER" = "mysql" ]; then
    # MySQL DSN: user:password@tcp(host:port)/dbname?params
    # Use Python to properly URL-encode password if it contains special characters
    if command -v python3 &> /dev/null; then
        ENCODED_PASSWORD=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$DB_PASSWORD', safe=''))")
    else
        ENCODED_PASSWORD="$DB_PASSWORD"
    fi
    # For golang-migrate, MySQL URL format: mysql://user:password@tcp(host:port)/dbname?query
    DB_URL="mysql://${DB_USER}:${ENCODED_PASSWORD}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?charset=utf8mb4&multiStatements=true"
else
    # PostgreSQL DSN: postgres://user:password@host:port/dbname?sslmode=disable
    if [ -n "$DB_URL" ]; then
        # If DB_URL already exists, ensure sslmode=disable is set (unless sslmode is already specified)
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
    else
        if command -v python3 &> /dev/null; then
            ENCODED_PASSWORD=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$DB_PASSWORD', safe=''))")
        else
            ENCODED_PASSWORD="$DB_PASSWORD"
        fi
        DB_URL="postgres://${DB_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
    fi
fi

# Execute migration based on command
case "$1" in
    up)
        echo "Running migrations up..."
        echo "DB_DRIVER: ${DB_DRIVER}"
        echo "DB_HOST: ${DB_HOST}"
        echo "DB_PORT: ${DB_PORT}"
        echo "DB_USER: ${DB_USER}"
        echo "DB_NAME: ${DB_NAME}"
        echo "MIGRATIONS_DIR: ${MIGRATIONS_DIR}"
        migrate -path ${MIGRATIONS_DIR} -database "${DB_URL}" up
        ;;
    down)
        echo "Running migrations down..."
        migrate -path ${MIGRATIONS_DIR} -database "${DB_URL}" down
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
        migrate -path ${MIGRATIONS_DIR} -database "${DB_URL}" version
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
        env migrate -path "${MIGRATIONS_DIR}" -database "${DB_URL}" force -- "$VERSION"
        ;;
    goto)
        if [ -z "$2" ]; then
            echo "Error: Version number is required"
            echo "Usage: $0 goto <version>"
            exit 1
        fi
        echo "Migrating to version $2..."
        migrate -path ${MIGRATIONS_DIR} -database "${DB_URL}" goto $2
        ;;
    *)
        echo "Usage: $0 {up|down|create <migration_name>|version|force <version>|goto <version>}"
        echo ""
        echo "Environment variables:"
        echo "  DB_DRIVER       postgres (default) or mysql"
        echo "  DB_HOST         Database host (default: localhost)"
        echo "  DB_PORT         Database port (default: 5432 for postgres, 3306 for mysql)"
        echo "  DB_USER         Database user"
        echo "  DB_PASSWORD     Database password"
        echo "  DB_NAME         Database name (default: WeKnora)"
        echo "  DB_URL          Full database URL (overrides individual settings)"
        echo "  MIGRATIONS_DIR  Migration files directory"
        exit 1
        ;;
esac

echo "Migration command completed successfully"

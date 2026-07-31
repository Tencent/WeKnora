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
DB_USER=${DB_USER:-postgres}
DB_PASSWORD=${DB_PASSWORD:-postgres}
DB_NAME=${DB_NAME:-WeKnora}

if [ -z "${DB_PORT:-}" ]; then
    case "$DB_DRIVER" in
        mysql)
            DB_PORT=3306
            ;;
        postgres)
            DB_PORT=5432
            ;;
    esac
fi

case "$DB_DRIVER" in
    postgres)
        DEFAULT_MIGRATIONS_DIR="migrations/versioned"
        ;;
    mysql)
        DEFAULT_MIGRATIONS_DIR="migrations/mysql"
        ;;
    sqlite)
        DEFAULT_MIGRATIONS_DIR="migrations/sqlite"
        ;;
    *)
        echo "Error: unsupported DB_DRIVER: ${DB_DRIVER}"
        echo "Supported values: postgres, mysql, sqlite"
        exit 1
        ;;
esac

MIGRATIONS_DIR="${MIGRATIONS_DIR:-$DEFAULT_MIGRATIONS_DIR}"

# Check if migrate tool is installed
if ! command -v migrate &> /dev/null; then
    echo "Error: migrate tool is not installed"
    echo "Install it with: go install -tags 'postgres mysql sqlite3' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"
    exit 1
fi

urlencode() {
    local raw="$1"
    local encoded=""
    local char
    local byte
    local i
    local LC_ALL=C
    for ((i = 0; i < ${#raw}; i++)); do
        char="${raw:i:1}"
        case "$char" in
            [a-zA-Z0-9.~_-])
                encoded+="$char"
                ;;
            *)
                printf -v byte '%%%02X' "'$char"
                encoded+="$byte"
                ;;
        esac
    done
    printf "%s" "$encoded"
}

append_query_param() {
    local url="$1"
    local param="$2"
    if [[ "$url" == *"?"* ]]; then
        printf "%s&%s" "$url" "$param"
    else
        printf "%s?%s" "$url" "$param"
    fi
}

set_query_param() {
    local url="$1"
    local key="$2"
    local value="$3"
    local base="$url"
    local query=""
    local result
    local part
    local separator="?"
    local -a parts=()

    if [[ "$url" == *"?"* ]]; then
        base="${url%%\?*}"
        query="${url#*\?}"
    fi

    result="$base"
    if [ -n "$query" ]; then
        IFS='&' read -r -a parts <<< "$query"
        for part in "${parts[@]}"; do
            if [ -z "$part" ] || [ "${part%%=*}" = "$key" ]; then
                continue
            fi
            result+="${separator}${part}"
            separator="&"
        done
    fi
    result+="${separator}${key}=${value}"
    printf "%s" "$result"
}

# Keep manual golang-migrate sessions aligned with the application startup
# contract in internal/database/mysql_config.go. Values are URL-encoded SQL
# literals because go-sql-driver applies them with SET on each connection.
MYSQL_TIME_ZONE_VALUE="%27%2B00%3A00%27"
MYSQL_SQL_MODE_VALUE="%27ONLY_FULL_GROUP_BY%2CSTRICT_TRANS_TABLES%2CNO_ZERO_IN_DATE%2CNO_ZERO_DATE%2CERROR_FOR_DIVISION_BY_ZERO%2CNO_ENGINE_SUBSTITUTION%27"

is_true() {
    # Character classes preserve case-insensitive parsing on Bash 3.2 (the
    # version still shipped by macOS); ${value,,} requires Bash 4 or newer.
    case "$1" in
        [Tt][Rr][Uu][Ee]) return 0 ;;
        [Ff][Aa][Ll][Ss][Ee]|"") return 1 ;;
        *)
            echo "Error: expected a boolean value, got: $1" >&2
            exit 1
            ;;
    esac
}

configure_mysql_transport() {
    # golang-migrate's MySQL driver supports x-tls-ca/cert/key only with a
    # named TLS config. tls=true uses system roots; tls=skip-verify is the
    # explicit insecure mode. Unlike the application driver, this CLI cannot
    # set a custom TLS ServerName, so reject that configuration rather than
    # silently verifying the wrong hostname.
    local tls_name=""
    local has_tls_ca=false
    local has_client_cert=false

    if [ -n "${DB_CONNECT_TIMEOUT:-}" ]; then
        DB_URL="$(set_query_param "$DB_URL" "timeout" "$(urlencode "$DB_CONNECT_TIMEOUT")")"
    fi
    if [ -n "${DB_READ_TIMEOUT:-}" ]; then
        DB_URL="$(set_query_param "$DB_URL" "readTimeout" "$(urlencode "$DB_READ_TIMEOUT")")"
    fi
    if [ -n "${DB_WRITE_TIMEOUT:-}" ]; then
        DB_URL="$(set_query_param "$DB_URL" "writeTimeout" "$(urlencode "$DB_WRITE_TIMEOUT")")"
    fi

    # A pre-existing DB_URL may already carry a named TLS config or the
    # standard tls=true/skip-verify policy. Preserve it unless the operator
    # explicitly sets DB_USE_TLS; silently replacing it with tls=false would
    # downgrade older deployments that predate the DB_TLS_* variables.
    if [ "$DB_URL_WAS_PROVIDED" = true ] && [ -z "${DB_USE_TLS:-}" ]; then
        if [ -n "${DB_TLS_SERVER_NAME:-}" ] || [ -n "${DB_TLS_CA:-}" ] || \
           [ -n "${DB_TLS_CERT:-}" ] || [ -n "${DB_TLS_KEY:-}" ] || \
           is_true "${DB_TLS_INSECURE_SKIP_VERIFY:-false}"; then
            echo "Error: DB_USE_TLS must be explicitly true before DB_TLS_* settings can be used." >&2
            exit 1
        fi
        return
    fi

    if ! is_true "${DB_USE_TLS:-false}"; then
        if [ -n "${DB_TLS_SERVER_NAME:-}" ] || [ -n "${DB_TLS_CA:-}" ] || \
           [ -n "${DB_TLS_CERT:-}" ] || [ -n "${DB_TLS_KEY:-}" ] || \
           is_true "${DB_TLS_INSECURE_SKIP_VERIFY:-false}"; then
            echo "Error: DB_USE_TLS must be true before DB_TLS_* settings can be used." >&2
            exit 1
        fi
        DB_URL="$(set_query_param "$DB_URL" "tls" "false")"
        return
    fi

    if [ -n "${DB_TLS_SERVER_NAME:-}" ]; then
        echo "Error: DB_TLS_SERVER_NAME is not supported by scripts/migrate.sh." >&2
        echo "Run startup migrations through the WeKnora app, or use DB_HOST matching the certificate DNS name." >&2
        exit 1
    fi

    if [ -n "${DB_TLS_CA:-}" ]; then
        if [ ! -r "$DB_TLS_CA" ]; then
            echo "Error: DB_TLS_CA is not a readable file: $DB_TLS_CA" >&2
            exit 1
        fi
        has_tls_ca=true
    fi

    if [ -n "${DB_TLS_CERT:-}" ] || [ -n "${DB_TLS_KEY:-}" ]; then
        if [ -z "${DB_TLS_CERT:-}" ] || [ -z "${DB_TLS_KEY:-}" ]; then
            echo "Error: DB_TLS_CERT and DB_TLS_KEY must be set together." >&2
            exit 1
        fi
        if [ ! -r "$DB_TLS_CERT" ] || [ ! -r "$DB_TLS_KEY" ]; then
            echo "Error: DB_TLS_CERT and DB_TLS_KEY must name readable files." >&2
            exit 1
        fi
        has_client_cert=true
    fi

    if [ "$has_client_cert" = true ] && [ "$has_tls_ca" = false ]; then
        echo "Error: scripts/migrate.sh requires DB_TLS_CA with client TLS credentials." >&2
        echo "The golang-migrate MySQL driver cannot combine a client certificate with system roots." >&2
        exit 1
    fi

    if [ "$has_tls_ca" = true ]; then
        tls_name="weknora-migrate"
        DB_URL="$(set_query_param "$DB_URL" "tls" "$tls_name")"
        DB_URL="$(set_query_param "$DB_URL" "x-tls-ca" "$(urlencode "$DB_TLS_CA")")"
        if [ "$has_client_cert" = true ]; then
            DB_URL="$(set_query_param "$DB_URL" "x-tls-cert" "$(urlencode "$DB_TLS_CERT")")"
            DB_URL="$(set_query_param "$DB_URL" "x-tls-key" "$(urlencode "$DB_TLS_KEY")")"
        fi
        if is_true "${DB_TLS_INSECURE_SKIP_VERIFY:-false}"; then
            DB_URL="$(set_query_param "$DB_URL" "x-tls-insecure-skip-verify" "true")"
        fi
    elif is_true "${DB_TLS_INSECURE_SKIP_VERIFY:-false}"; then
        DB_URL="$(set_query_param "$DB_URL" "tls" "skip-verify")"
    else
        DB_URL="$(set_query_param "$DB_URL" "tls" "true")"
    fi

}

# Construct the database URL.
# If DB_URL is already set in .env, use it and normalize required local defaults.
# Otherwise, construct it from individual components.
DB_URL_WAS_PROVIDED=false
if [ -n "${DB_URL:-}" ]; then
    DB_URL_WAS_PROVIDED=true
    if [ "$DB_DRIVER" = "postgres" ]; then
        if [[ "$DB_URL" != *"sslmode="* ]]; then
            DB_URL="$(append_query_param "$DB_URL" "sslmode=disable")"
        elif [[ "$DB_URL" == *"sslmode=require"* ]] || [[ "$DB_URL" == *"sslmode=prefer"* ]]; then
            DB_URL="${DB_URL//sslmode=require/sslmode=disable}"
            DB_URL="${DB_URL//sslmode=prefer/sslmode=disable}"
        fi
    elif [ "$DB_DRIVER" = "mysql" ]; then
        DB_URL="$(set_query_param "$DB_URL" "charset" "utf8mb4")"
        DB_URL="$(set_query_param "$DB_URL" "multiStatements" "true")"
        DB_URL="$(set_query_param "$DB_URL" "parseTime" "true")"
        DB_URL="$(set_query_param "$DB_URL" "loc" "UTC")"
        DB_URL="$(set_query_param "$DB_URL" "time_zone" "$MYSQL_TIME_ZONE_VALUE")"
        DB_URL="$(set_query_param "$DB_URL" "sql_mode" "$MYSQL_SQL_MODE_VALUE")"
    fi
else
    ENCODED_USER=$(urlencode "$DB_USER")
    ENCODED_PASSWORD=$(urlencode "$DB_PASSWORD")
    ENCODED_DB_NAME=$(urlencode "$DB_NAME")
    case "$DB_DRIVER" in
        postgres)
            DB_URL="postgres://${ENCODED_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${ENCODED_DB_NAME}?sslmode=disable"
            ;;
        mysql)
            DB_URL="mysql://${ENCODED_USER}:${ENCODED_PASSWORD}@tcp(${DB_HOST}:${DB_PORT})/${ENCODED_DB_NAME}?charset=utf8mb4&multiStatements=true&parseTime=true&loc=UTC&time_zone=${MYSQL_TIME_ZONE_VALUE}&sql_mode=${MYSQL_SQL_MODE_VALUE}"
            ;;
        sqlite)
            DB_PATH=${DB_PATH:-./data/weknora.db}
            DB_URL="sqlite3://${DB_PATH}"
            ;;
esac
fi

if [ "$DB_DRIVER" = "mysql" ]; then
    configure_mysql_transport
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
        # Use env to pass the command, avoiding shell flag parsing issues with negative numbers.
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

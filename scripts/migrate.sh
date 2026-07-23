#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

if [ -f "${PROJECT_ROOT}/.env" ]; then
    echo "Loading .env file from ${PROJECT_ROOT}/.env"
    set -a
    source "${PROJECT_ROOT}/.env"
    set +a
fi

DB_DRIVER="${DB_DRIVER:-postgres}"
case "${DB_DRIVER}" in
    postgres)
        DB_PORT_DEFAULT=5432
        DB_USER_DEFAULT=postgres
        DB_PASSWORD_DEFAULT=postgres
        MIGRATIONS_DIR_DEFAULT="migrations/versioned"
        ;;
    mysql)
        DB_PORT_DEFAULT=3306
        DB_USER_DEFAULT=weknora
        DB_PASSWORD_DEFAULT=""
        MIGRATIONS_DIR_DEFAULT="migrations/mysql"
        ;;
    *)
        echo "Error: unsupported DB_DRIVER='${DB_DRIVER}' (expected 'postgres' or 'mysql')" >&2
        exit 1
        ;;
esac

DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-${DB_PORT_DEFAULT}}"
DB_USER="${DB_USER:-${DB_USER_DEFAULT}}"
DB_PASSWORD="${DB_PASSWORD:-${DB_PASSWORD_DEFAULT}}"
DB_NAME="${DB_NAME:-WeKnora}"
DB_URL="${DB_URL:-}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-${MIGRATIONS_DIR_DEFAULT}}"

require_migrate() {
    if ! command -v migrate >/dev/null 2>&1; then
        echo "Error: migrate tool is not installed" >&2
        echo "Install: go install -tags 'postgres mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1" >&2
        exit 1
    fi
}

build_database_url() {
    if ! command -v python3 >/dev/null 2>&1; then
        echo "Error: python3 is required to build the database URL safely" >&2
        exit 1
    fi

    export DB_DRIVER DB_HOST DB_PORT DB_USER DB_PASSWORD DB_NAME DB_URL
    export DB_SSLMODE DB_CONNECT_TIMEOUT DB_READ_TIMEOUT DB_WRITE_TIMEOUT
    export DB_USE_TLS DB_TLS_SERVER_NAME DB_TLS_CA DB_TLS_CERT DB_TLS_KEY
    export DB_TLS_INSECURE_SKIP_VERIFY

    python3 <<'PY'
import os
import sys
from urllib.parse import parse_qsl, quote, urlencode


def env(name, default=""):
    value = os.environ.get(name, "").strip()
    return value or default


def parse_bool(name, default=False):
    value = env(name)
    if not value:
        return default
    normalized = value.lower()
    if normalized in {"1", "t", "true"}:
        return True
    if normalized in {"0", "f", "false"}:
        return False
    raise SystemExit(f"Error: {name} must be true or false, got {value!r}")


def normalize_database_url(url, driver):
    valid_scheme = (
        driver == "postgres" and url.startswith(("postgres://", "postgresql://"))
    ) or (driver == "mysql" and url.startswith("mysql://"))
    if not valid_scheme:
        raise SystemExit(f"Error: DB_URL scheme does not match DB_DRIVER={driver!r}")

    base, separator, raw_query = url.rpartition("?")
    if not separator:
        base = url
        raw_query = ""
    params = parse_qsl(raw_query, keep_blank_values=True)

    if driver == "postgres":
        sslmode_indexes = [index for index, (key, _) in enumerate(params) if key == "sslmode"]
        if len(sslmode_indexes) > 1:
            raise SystemExit("Error: PostgreSQL DB_URL must not repeat sslmode")
        if not sslmode_indexes:
            params.append(("sslmode", env("DB_SSLMODE", "disable")))
        return f"{base}?{urlencode(params)}"

    def require_parameter(name, expected, *, case_insensitive=False):
        indexes = [index for index, (key, _) in enumerate(params) if key == name]
        if len(indexes) > 1:
            raise SystemExit(f"Error: MySQL DB_URL must not repeat {name}")
        if not indexes:
            params.append((name, expected))
            return

        index = indexes[0]
        value = params[index][1]
        matches = value.lower() == expected.lower() if case_insensitive else value == expected
        if not matches:
            raise SystemExit(f"Error: MySQL DB_URL must set {name}={expected}")
        params[index] = (name, expected)

    require_parameter("multiStatements", "true", case_insensitive=True)
    require_parameter("time_zone", "'+00:00'")
    return f"{base}?{urlencode(params)}"


driver = env("DB_DRIVER", "postgres").lower()
database_url = env("DB_URL")
if database_url:
    print(normalize_database_url(database_url, driver))
    sys.exit(0)

host = env("DB_HOST", "localhost")
port = env("DB_PORT", "5432" if driver == "postgres" else "3306")
user = env("DB_USER", "postgres" if driver == "postgres" else "weknora")
password = os.environ.get("DB_PASSWORD", "")
database = env("DB_NAME", "WeKnora")

if not password:
    raise SystemExit("Error: DB_PASSWORD is required when DB_URL is not set")
if not host or not user or not database:
    raise SystemExit("Error: DB_HOST, DB_USER, and DB_NAME must be non-empty")
try:
    port_number = int(port)
except ValueError as exc:
    raise SystemExit(f"Error: DB_PORT must be an integer, got {port!r}") from exc
if not 1 <= port_number <= 65535:
    raise SystemExit(f"Error: DB_PORT must be between 1 and 65535, got {port!r}")

encoded_user = quote(user, safe="")
encoded_password = quote(password, safe="")
encoded_database = quote(database, safe="")
network_host = host
if ":" in network_host and not network_host.startswith("["):
    network_host = f"[{network_host}]"

if driver == "postgres":
    sslmode = env("DB_SSLMODE", "disable")
    query = urlencode({"sslmode": sslmode})
    print(
        f"postgres://{encoded_user}:{encoded_password}@"
        f"{network_host}:{port_number}/{encoded_database}?{query}"
    )
    sys.exit(0)

if driver != "mysql":
    raise SystemExit(f"Error: unsupported DB_DRIVER={driver!r}")

params = {
    "multiStatements": "true",
    "parseTime": "true",
    "loc": "UTC",
    "charset": "utf8mb4",
    "collation": "utf8mb4_0900_ai_ci",
    "time_zone": "'+00:00'",
    "timeout": env("DB_CONNECT_TIMEOUT", "10s"),
    "readTimeout": env("DB_READ_TIMEOUT", "30s"),
    "writeTimeout": env("DB_WRITE_TIMEOUT", "30s"),
}

use_tls = parse_bool("DB_USE_TLS")
insecure = parse_bool("DB_TLS_INSECURE_SKIP_VERIFY")
server_name = env("DB_TLS_SERVER_NAME")
ca_file = env("DB_TLS_CA")
cert_file = env("DB_TLS_CERT")
key_file = env("DB_TLS_KEY")
has_tls_settings = bool(server_name or ca_file or cert_file or key_file or insecure)

if not use_tls and has_tls_settings:
    raise SystemExit("Error: DB_USE_TLS must be true when DB_TLS_* settings are configured")
if bool(cert_file) != bool(key_file):
    raise SystemExit("Error: DB_TLS_CERT and DB_TLS_KEY must be configured together")
if server_name and server_name.strip("[]").casefold() != host.strip("[]").casefold():
    raise SystemExit(
        "Error: migrate CLI cannot use DB_TLS_SERVER_NAME different from DB_HOST; "
        "provide a DB_URL whose host matches the certificate or use application auto-migration"
    )

if use_tls:
    if ca_file:
        params["tls"] = "custom"
        params["x-tls-ca"] = ca_file
        if cert_file:
            params["x-tls-cert"] = cert_file
            params["x-tls-key"] = key_file
        if insecure:
            params["x-tls-insecure-skip-verify"] = "true"
    elif cert_file:
        raise SystemExit("Error: migrate CLI requires DB_TLS_CA when mTLS certificates are configured")
    elif insecure:
        params["tls"] = "skip-verify"
    else:
        params["tls"] = "true"

query = urlencode(params)
print(
    f"mysql://{encoded_user}:{encoded_password}@"
    f"tcp({network_host}:{port_number})/{encoded_database}?{query}"
)
PY
}

run_database_migration() {
    require_migrate
    local database_url
    database_url="$(build_database_url)"
    (
        cd "${PROJECT_ROOT}"
        migrate -path "${MIGRATIONS_DIR}" -database "${database_url}" "$@"
    )
}

case "${1:-}" in
    up)
        echo "Running migrations up..."
        echo "DB_DRIVER: ${DB_DRIVER}"
        echo "DB_HOST: ${DB_HOST}"
        echo "DB_PORT: ${DB_PORT}"
        echo "DB_NAME: ${DB_NAME}"
        echo "MIGRATIONS_DIR: ${MIGRATIONS_DIR}"
        run_database_migration up
        ;;
    down)
        echo "Running migrations down..."
        run_database_migration down
        ;;
    version)
        echo "Checking current migration version..."
        run_database_migration version
        ;;
    force)
        if [ -z "${2:-}" ]; then
            echo "Error: Version number is required" >&2
            echo "Usage: $0 force <version>" >&2
            exit 1
        fi
        echo "Forcing migration version to ${2}..."
        run_database_migration force -- "${2}"
        ;;
    goto)
        if [ -z "${2:-}" ]; then
            echo "Error: Version number is required" >&2
            echo "Usage: $0 goto <version>" >&2
            exit 1
        fi
        echo "Migrating to version ${2}..."
        run_database_migration goto "${2}"
        ;;
    create)
        if [ -z "${2:-}" ]; then
            echo "Error: Migration name is required" >&2
            echo "Usage: $0 create <migration_name>" >&2
            exit 1
        fi
        if [ "${DB_DRIVER}" = "mysql" ]; then
            echo "Error: MySQL schema changes must be folded into migrations/mysql/000000_init.up.sql and its matching down baseline" >&2
            exit 1
        fi
        require_migrate
        echo "Creating migration files for ${2}..."
        (
            cd "${PROJECT_ROOT}"
            migrate create -ext sql -dir "${MIGRATIONS_DIR}" -seq "${2}"
        )
        ;;
    *)
        echo "Usage: $0 {up|down|version|force <version>|goto <version>|create <migration_name>}" >&2
        exit 1
        ;;
esac

echo "Migration command completed successfully"

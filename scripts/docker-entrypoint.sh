#!/bin/bash
set -e

# ─── Fix ownership of bind-mounted directories ───
# When users bind-mount host directories (e.g. ./skills/preloaded),
# the mount inherits the host UID/GID which may differ from the
# container's appuser. This entrypoint runs as root, fixes ownership,
# then drops privileges to appuser via gosu — the same pattern used
# by official postgres/redis images.

# Directories that may be bind-mounted and need appuser access
MOUNT_DIRS=(
    /app/skills/preloaded
    /data/files
)

for dir in "${MOUNT_DIRS[@]}"; do
    if [ -d "$dir" ]; then
        chown -R appuser:appuser "$dir" 2>/dev/null || true
    fi
done

# ─── Merge built-in skills into preloaded ───
# Built-in skills are backed up at /app/skills/_builtin during image build.
# After a bind-mount replaces /app/skills/preloaded, copy back any
# missing built-in skills (without overwriting user-provided ones).
BUILTIN_DIR="/app/skills/_builtin"
PRELOADED_DIR="/app/skills/preloaded"

if [ -d "$BUILTIN_DIR" ]; then
    mkdir -p "$PRELOADED_DIR"
    for skill_dir in "$BUILTIN_DIR"/*/; do
        [ -d "$skill_dir" ] || continue
        skill_name="$(basename "$skill_dir")"
        if [ ! -d "$PRELOADED_DIR/$skill_name" ]; then
            cp -r "$skill_dir" "$PRELOADED_DIR/$skill_name"
        fi
    done
    chown -R appuser:appuser "$PRELOADED_DIR"
fi

# ─── Wait for the configured primary database ───
# Compose health dependencies cannot be conditional on DB_DRIVER. Waiting here
# supports PostgreSQL, MySQL, and external managed databases without keeping an
# unused database container alive.
wait_for_database() {
    local driver="${DB_DRIVER:-postgres}"
    local host="${DB_HOST:-}"
    local port="${DB_PORT:-}"
    local attempts="${DB_STARTUP_MAX_ATTEMPTS:-60}"
    local interval="${DB_STARTUP_RETRY_INTERVAL:-2}"

    case "$driver" in
        sqlite)
            return 0
            ;;
        postgres)
            host="${host:-postgres}"
            port="${port:-5432}"
            ;;
        mysql)
            host="${host:-mysql}"
            port="${port:-3306}"
            ;;
        *)
            echo "Unsupported DB_DRIVER: $driver" >&2
            return 1
            ;;
    esac

    echo "Waiting for $driver database at $host:$port..."
    local attempt
    for attempt in $(seq 1 "$attempts"); do
        if [ "$driver" = "postgres" ]; then
            if PGPASSWORD="${DB_PASSWORD:-}" pg_isready \
                -h "$host" -p "$port" -U "${DB_USER:-postgres}" -d "${DB_NAME:-WeKnora}" >/dev/null 2>&1; then
                echo "PostgreSQL is ready"
                return 0
            fi
        else
            if MYSQL_PWD="${DB_PASSWORD:-}" mysqladmin ping \
                -h "$host" -P "$port" -u "${DB_USER:-root}" --silent >/dev/null 2>&1; then
                echo "MySQL is ready"
                return 0
            fi
        fi
        sleep "$interval"
    done

    echo "Timed out waiting for $driver database at $host:$port after $attempts attempts" >&2
    return 1
}

wait_for_database

# ─── Drop privileges and exec the main process ───
exec gosu appuser "$@"

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

# Wait for the configured primary database. Credentials are passed through
# environment variables only, so they never appear in the command line or log.
wait_for_database() {
    local driver="${DB_DRIVER:-postgres}"
    local host="${DB_HOST:-postgres}"
    local port="${DB_PORT:-5432}"
    local deadline=$((SECONDS + 60))

    case "$driver" in
        sqlite)
            return 0
            ;;
        postgres|mysql)
            ;;
        *)
            echo "Unsupported DB_DRIVER: $driver" >&2
            return 1
            ;;
    esac

    echo "Waiting for $driver database at $host:$port..."
    while [ "$SECONDS" -lt "$deadline" ]; do
        if [ "$driver" = "postgres" ]; then
            if pg_isready -q -h "$host" -p "$port" -U "${DB_USER:-postgres}" -d "${DB_NAME:-WeKnora}"; then
                echo "PostgreSQL database is ready."
                return 0
            fi
        elif MYSQL_PWD="${DB_PASSWORD:-}" mysqladmin ping \
            -h "$host" -P "$port" -u "${DB_USER:-root}" --silent >/dev/null 2>&1; then
            echo "MySQL database is ready."
            return 0
        fi
        sleep 2
    done

    echo "Timed out after 60 seconds waiting for $driver database at $host:$port" >&2
    return 1
}

wait_for_database

# ─── Drop privileges and exec the main process ───
exec gosu appuser "$@"

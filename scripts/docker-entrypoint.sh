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

# Compose cannot conditionally depend on postgres or mysql. Wait only for the
# configured business database here; this also supports external databases.
validate_database_retriever() {
    local db_driver retrieve_driver
    db_driver="$(printf '%s' "${DB_DRIVER:-postgres}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"
    retrieve_driver="$(printf '%s' "${RETRIEVE_DRIVER:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"
    case ",$retrieve_driver," in
        *,mysql,*)
            echo "[entrypoint] RETRIEVE_DRIVER=mysql is not supported" >&2
            return 1
            ;;
    esac
    if [ "$db_driver" = "mysql" ] && [ "$retrieve_driver" != "qdrant" ]; then
        echo "[entrypoint] DB_DRIVER=mysql requires RETRIEVE_DRIVER=qdrant" >&2
        return 1
    fi
}

wait_for_database() {
    local driver
    driver="$(printf '%s' "${DB_DRIVER:-postgres}" | tr '[:upper:]' '[:lower:]')"

    case "$driver" in
        postgres)
            export DB_HOST="${DB_HOST:-postgres}"
            export DB_PORT="${DB_PORT:-5432}"
            ;;
        mysql)
            export DB_HOST="${DB_HOST:-mysql}"
            export DB_PORT="${DB_PORT:-3306}"
            ;;
        sqlite)
            return 0
            ;;
        *)
            echo "[entrypoint] unsupported DB_DRIVER: ${DB_DRIVER:-}" >&2
            return 1
            ;;
    esac

    local timeout="${DB_STARTUP_WAIT_TIMEOUT:-120}"
    local started_at=$SECONDS
    echo "[entrypoint] waiting for ${driver} at ${DB_HOST}:${DB_PORT}..."
    while true; do
        if [ "$driver" = "mysql" ]; then
            if MYSQL_PWD="${DB_PASSWORD:-}" mysqladmin \
                --protocol=tcp --host="$DB_HOST" --port="$DB_PORT" \
                --user="${DB_USER:-root}" ping --silent >/dev/null 2>&1; then
                break
            fi
        elif pg_isready --host="$DB_HOST" --port="$DB_PORT" \
            --username="${DB_USER:-postgres}" >/dev/null 2>&1; then
            break
        fi

        if [ $((SECONDS - started_at)) -ge "$timeout" ]; then
            echo "[entrypoint] ${driver} did not become ready within ${timeout}s" >&2
            return 1
        fi
        sleep 2
    done
    echo "[entrypoint] ${driver} is ready"
}

validate_database_retriever
wait_for_database

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

# ─── Drop privileges and exec the main process ───
exec gosu appuser "$@"

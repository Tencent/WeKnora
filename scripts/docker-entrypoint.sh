#!/bin/bash
set -e

# ─── Fix ownership of bind-mounted directories ───
# When users bind-mount host directories (e.g. ./data/files),
# the mount inherits the host UID/GID which may differ from the
# container's appuser. This entrypoint runs as root, fixes ownership,
# then drops privileges to appuser via gosu — the same pattern used
# by official postgres/redis images.

# Directories that may be bind-mounted and need appuser access
MOUNT_DIRS=(
    /data/files
)

# ─── Docker socket access for the sandbox backend ───
# The Engine API socket is typically root:docker 0660. Docker Desktop exposes
# its socket inside Linux containers as root:root 0660, so GID 0 is valid when
# the group bits really grant read/write access. This process drops to appuser
# via gosu, which calls initgroups and therefore drops compose group_add. Match
# the socket's GID in /etc/group before gosu. Never chmod or chown the mounted
# socket: that would change the security boundary outside this container.
grant_docker_sock_to_appuser() {
    local sock="$1"
    local gid mode grp
    [ -S "$sock" ] || return 0
    if gosu appuser test -r "$sock" 2>/dev/null &&
        gosu appuser test -w "$sock" 2>/dev/null; then
        return 0
    fi
    gid="$(stat -c '%g' -- "$sock" 2>/dev/null || true)"
    mode="$(stat -c '%a' -- "$sock" 2>/dev/null || true)"
    if [ -z "$gid" ] || [ -z "$mode" ]; then
        echo "weknora: cannot stat $sock; Docker sandbox may be unable to reach the daemon" >&2
        return 0
    fi
    case "$mode" in
        *[!0-7]*)
            echo "weknora: invalid mode $mode for $sock; Docker sandbox may be unable to reach the daemon" >&2
            return 0
            ;;
    esac
    if (( (8#$mode & 0060) != 0060 )); then
        echo "weknora: $sock is not group-readable and group-writable; refusing to change socket permissions" >&2
        return 0
    fi
    if ! getent group "$gid" >/dev/null 2>&1; then
        if ! groupadd -g "$gid" dockersock >/dev/null 2>&1; then
            echo "weknora: failed to create group for $sock GID $gid; Docker sandbox may be unable to reach the daemon" >&2
            return 0
        fi
    fi
    grp="$(getent group "$gid" | cut -d: -f1)"
    if [ -z "$grp" ]; then
        echo "weknora: no group name for GID $gid on $sock" >&2
        return 0
    fi
    if ! usermod -aG "$grp" appuser >/dev/null 2>&1; then
        echo "weknora: failed to add appuser to $grp for $sock; Docker sandbox may be unable to reach the daemon" >&2
        return 0
    fi
}

main() {
    local dir
    for dir in "${MOUNT_DIRS[@]}"; do
        if [ -d "$dir" ]; then
            chown -R appuser:appuser "$dir" 2>/dev/null || true
        fi
    done

    grant_docker_sock_to_appuser /var/run/docker.sock
    case "${DOCKER_HOST:-}" in
        unix://*)
            grant_docker_sock_to_appuser "${DOCKER_HOST#unix://}"
            ;;
    esac

    # ─── Drop privileges and exec the main process ───
    exec gosu appuser "$@"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi

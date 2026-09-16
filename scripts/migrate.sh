#!/usr/bin/env bash
set -euo pipefail
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command_name="${1:-inspect}"
if [ "$#" -gt 0 ]; then shift; fi
if [ -n "${WEKNORA_MIGRATE_BIN:-}" ]; then
    exec "$WEKNORA_MIGRATE_BIN" "$command_name" --root "$PROJECT_ROOT/migrations" "$@"
elif [ -x "$PROJECT_ROOT/weknora-migrate" ]; then
    exec "$PROJECT_ROOT/weknora-migrate" "$command_name" --root "$PROJECT_ROOT/migrations" "$@"
elif command -v weknora-migrate >/dev/null 2>&1; then
    exec weknora-migrate "$command_name" --root "$PROJECT_ROOT/migrations" "$@"
elif command -v go >/dev/null 2>&1; then
    cd "$PROJECT_ROOT"
    exec go run -tags sqlite_fts5 ./cmd/migrate-runner "$command_name" --root "$PROJECT_ROOT/migrations" "$@"
fi
printf '%s\n' 'weknora-migrate is required; build ./cmd/migrate-runner with sqlite_fts5.' >&2
exit 1

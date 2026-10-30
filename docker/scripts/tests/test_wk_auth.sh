#!/usr/bin/env bash
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/sync" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" > "$WK_AUTH_TEST_LOG"
STUB
chmod +x "$TMP/sync"
export WK_SYNC_CMD="$TMP/sync" WK_AUTH_TEST_LOG="$TMP/log" WEKNORA_BROWSER_ROOT="$TMP/session.browser"
bash "$HERE/../wk-auth.sh" save
test "$(cat "$TMP/log")" = "--capture $WEKNORA_BROWSER_ROOT"
if bash "$HERE/../wk-auth.sh" save https://example.com 2>/dev/null; then exit 1; fi
printf '%s\n' 'ok wk-auth delegates to the browser sync capture'

#!/usr/bin/env bash
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
bash -n "$HERE/../wk-browser-sync.sh"
for module in cdp-cookies wk-browser-sync wk-cookie-plan wk-page-sync; do
  node --check "$HERE/../$module.mjs"
done
node --test "$HERE/test_browser_sync.mjs"

# The daemon form holds the machine lock; a second daemon exits at once.
export WK_BROWSER_AUTH_DIR="$TMP/auth" WK_BROWSER_LIB_DIR="$TMP/lib" WK_NODE="$TMP/node"
mkdir -p "$TMP/lib"
printf '#!/bin/sh\nsleep 5\n' > "$TMP/node"
chmod +x "$TMP/node"
bash "$HERE/../wk-browser-sync.sh" & first=$!
sleep 0.3
timeout 2 bash "$HERE/../wk-browser-sync.sh"
kill "$first" 2>/dev/null || true
test "$(stat -c %a "$TMP/auth")" = 700
printf '%s\n' 'ok wk-browser-sync runs one daemon per machine'

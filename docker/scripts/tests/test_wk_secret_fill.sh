#!/usr/bin/env bash
# docker/scripts/tests/test_wk_secret_fill.sh
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
bash -n "$HERE/../wk-secret-fill.sh"
node --check "$HERE/../wk-secret-fill.mjs"
node --test "$HERE/test_secret_fill.mjs"

# The fill mode holds the session's command lock; the probe never waits on it.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
printf '#!/bin/sh\necho "{\\"url\\":null}"\n' > "$TMP/node"
chmod +x "$TMP/node"
export WK_NODE="$TMP/node" WK_BROWSER_LIB_DIR="$TMP" WEKNORA_BROWSER_ROOT="$TMP/br"
mkdir -p "$WEKNORA_BROWSER_ROOT"
exec 9>"$WEKNORA_BROWSER_ROOT/.command.lock"
flock 9
out="$(timeout 5 bash "$HERE/../wk-secret-fill.sh" --probe)"
test "$out" = '{"url":null}'
exec 9>&-
printf '%s\n' 'ok wk-secret-fill'

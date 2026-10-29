#!/usr/bin/env bash
# Machine-wide login state sync for the session browsers. With no arguments it
# runs as the daemon (a second copy exits via flock); --restore-root and
# --capture are one-shot passes for the wrapper and wk-auth.
set -u
umask 077
LIB="${WK_BROWSER_LIB_DIR:-/usr/local/lib/weknora}"
AUTH="${WK_BROWSER_AUTH_DIR:-/var/lib/weknora/browser/auth}"
export WK_BROWSER_AUTH_DIR="$AUTH"
mkdir -p "$AUTH" || exit 1
chmod 0700 "$AUTH"
if [ $# -eq 0 ]; then
  exec 8>"$AUTH/.sync.lock"
  flock -n 8 || exit 0
fi
cd /
exec "${WK_NODE:-/usr/local/bin/node}" "$LIB/wk-browser-sync.mjs" "$@"

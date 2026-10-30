#!/usr/bin/env bash
# One machine-wide fingerprint governor: UA / Client Hints, hardware
# concurrency and egress-matched time zone for every session browser.
set -u
umask 077
LIB="${WK_MERGE_LIB_DIR:-/usr/local/lib/weknora}"
LOCK_DIR="${WK_GOVERNOR_LOCK_DIR:-/var/lib/weknora/browser/auth}"
mkdir -p "$LOCK_DIR" || exit 1
exec 8>"$LOCK_DIR/.ua-governor.lock"
flock -n 8 || exit 0
cd /
exec "${WK_NODE:-/usr/local/bin/node}" "$LIB/wk-ua-governor.mjs" "$@"

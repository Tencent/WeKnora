#!/usr/bin/env bash
# Saves this session browser's current login state into the machine store now,
# instead of waiting for the next sync pass.
set -eu
[ "${1:-}" = save ] && [ $# -eq 1 ] || {
  echo 'usage: wk-auth save' >&2
  exit 2
}
[ -n "${WEKNORA_BROWSER_ROOT:-}" ] || {
  echo 'wk-auth: WEKNORA_BROWSER_ROOT is required' >&2
  exit 70
}
exec "${WK_SYNC_CMD:-/usr/local/bin/wk-browser-sync}" --capture "$WEKNORA_BROWSER_ROOT"

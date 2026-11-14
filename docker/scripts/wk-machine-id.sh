#!/bin/sh
# Stable machine identity for the browser. /etc/machine-id lives in the
# container layer and changes on every rebuild; a fresh id makes sites treat
# every existing login as "new device". The identity is kept in the
# persistent browser state tree and written back to /etc/machine-id before
# every browser launch.
#
# Always succeeds and never prints: an identity problem must not delay or
# fail a browser start.
PERSIST="${WK_MACHINE_ID:-/var/lib/weknora/browser/machine-id}"
ETC="${WK_MACHINE_ID_ETC:-/etc/machine-id}"

mkdir -p "$(dirname "$PERSIST")" 2>/dev/null || exit 0

is_hex_id() { case "$1" in ''|*[!0-9a-f]*) return 1 ;; esac; [ "${#1}" -ge 16 ]; }

if [ -s "$PERSIST" ]; then
  id="$(cat "$PERSIST" 2>/dev/null || true)"
  if is_hex_id "$id"; then
    cur="$(cat "$ETC" 2>/dev/null || true)"
    [ "$cur" = "$id" ] || printf '%s\n' "$id" > "$ETC" 2>/dev/null || true
    exit 0
  fi
fi

# First run on this machine: adopt the live id if it looks sane, else mint one.
id="$(cat "$ETC" 2>/dev/null || true)"
is_hex_id "$id" || id=""
[ -n "$id" ] || { command -v dbus-uuidgen >/dev/null 2>&1 && id="$(dbus-uuidgen 2>/dev/null || true)"; }
[ -n "$id" ] || exit 0
printf '%s\n' "$id" > "$PERSIST" 2>/dev/null || exit 0
exit 0

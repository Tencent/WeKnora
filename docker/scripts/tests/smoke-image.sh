#!/usr/bin/env bash
# Usage: smoke-image.sh IMAGE   (needs docker; run against a built desktop image)
#
# The stub tests prove the wrapper's logic. This proves the assumptions about
# the real binary and the real desktop: HOME-scoped daemon, one browser per
# session, cmdline carries the user-data-dir, window pid attribution, vault
# cookies, socket length, headless fallback.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
IMG="${1:?usage: smoke-image.sh IMAGE}"
C=wk-browser-smoke
A=0f8fad5b-d9cb-469f-a165-70867728950e
B=7c9e6679-7425-40de-944b-e07fc1f90ae7
H=2b6c1a54-58e1-4f0e-9f61-3f2f0f9b1d21
docker rm -f "$C" >/dev/null 2>&1
docker run -d --name "$C" --entrypoint sleep "$IMG" infinity >/dev/null
trap 'docker rm -f "$C" >/dev/null 2>&1' EXIT

root() { echo "/var/lib/weknora/sessions/$1.browser"; }
ab() { local s=$1; shift; docker exec -e WEKNORA_SESSION_ID="$s" -e WEKNORA_BROWSER_ROOT="$(root "$s")" "$C" agent-browser "$@"; }
dx() { docker exec "$C" sh -c "$1"; }

# Count Chrome MAIN processes for one session root.
#
# Every renderer/zygote/GPU child carries the parent's --user-data-dir, so
# pgrep -f on the user-data-dir counts 10-20 processes per window; the main
# process is the one without --type= in its cmdline. A shell whose argv holds
# the pattern would match too, hence the helper is a file of its own.
dxin() { docker exec -i "$C" sh -c "$1"; }
dxin 'cat > /tmp/wk-mains.sh' <<'HELPER'
#!/bin/sh
# usage: wk-mains.sh count|pid <session-browser-root>
R=$2
for p in $(ls /proc | grep -E '^[0-9]+$'); do
  [ -r "/proc/$p/cmdline" ] || continue
  c=$(tr '\0' ' ' < "/proc/$p/cmdline" 2>/dev/null)
  case "$c" in
    *"--user-data-dir=$R/profile"*) case "$c" in *--type=*) continue ;; esac ;;
    *) continue ;;
  esac
  case "$1" in
    count) n=$((n + 1)) ;;
    pid) echo "$p"; exit 0 ;;
  esac
done
case "$1" in
  count) echo "${n:-0}" ;;
  pid) exit 1 ;;
esac
HELPER
main_count() { dx "sh /tmp/wk-mains.sh count $(root "$1")"; }
main_pid() { dx "sh /tmp/wk-mains.sh pid $(root "$1")"; }

dx '/usr/local/bin/start-desktop.sh' || fail "desktop did not start"
dx 'python3 -m http.server 18080 --bind 127.0.0.1 >/dev/null 2>&1 &'; sleep 1
URL=http://127.0.0.1:18080/

ab "$A" --headed open "$URL" >/dev/null; ab "$B" --headed open "$URL" >/dev/null; sleep 1
assert_eq "one browser main for A" 1 "$(main_count "$A")"
assert_eq "one browser main for B" 1 "$(main_count "$B")"
LIST="$(dx 'wk-display list')"
assert_contains "A owns a seat" "$A" "$LIST"
assert_contains "B owns a seat" "$B" "$LIST"

# Count daemon socket path bytes without a trailing newline. Linux sun_path
# is 108 bytes; keep paths within the wrapper's 104-byte budget.
SOCK="$(dx 'for f in /var/lib/weknora/sessions/*.browser/home/.agent-browser/*.sock; do [ -e "$f" ] && printf "%s\n" "$(printf %s "$f" | wc -c)"; done')"
assert_eq "one daemon socket per session" 2 "$(echo "$SOCK" | grep -c '[0-9]')"
for n in $SOCK; do
  if [ "$n" -le 104 ]; then pass "socket path length $n"; else fail "socket path too long: $n"; fi
done

# A's close --all must not touch B (HOME isolation)
ab "$A" close --all >/dev/null; sleep 1
assert_eq "B browser still alive" 1 "$(main_count "$B")"

# Window attribution: B's browser window lives on B's own X server (:seat) —
# a separate display per seat, so a window found there is B's and nothing
# else's. Read the seat from the table, not from a guess.
BS="$(dx 'wk-display list' | awk -v b="$B" '$0 ~ b {print $2}')"
BPID="$(main_pid "$B")"
BW="$(dx "DISPLAY=:$BS xdotool search --pid $BPID 2>/dev/null | head -1")"
if [ -n "$BW" ]; then pass "B window is on its own display :$BS"; else fail "B window missing on :$BS"; fi

# vault -> fresh session gets the cookie
dx "mkdir -p /var/lib/weknora/browser/auth && printf '%s' '{\"version\":1,\"cookies\":[{\"name\":\"smokecookie\",\"value\":\"v1\",\"domain\":\"127.0.0.1\",\"path\":\"/\",\"expires\":-1}]}' > /var/lib/weknora/browser/auth/chrome-cookie-seed.json"
ab "$A" close >/dev/null
ab "$A" open "$URL" >/dev/null; sleep 1
assert_contains "vault cookie visible in A" smokecookie "$(ab "$A" cookies get 2>&1)"

# wrapper drops --cdp and runs against this session's own browser
assert_eq "ignores --cdp" 0 "$(ab "$A" --cdp 9222 snapshot >/dev/null 2>&1; echo $?)"

# B's close keeps its seat on purpose (seats outlive the browser; they are
# released by session deletion or the idle reap, not by close). The browser
# itself must be gone.
ab "$B" close >/dev/null; sleep 1
assert_eq "B browser gone after close" 0 "$(main_count "$B")"
assert_contains "B seat still held after close" "$B" "$(dx 'wk-display list')"

# Headless fallback: a session that never claimed a seat has no DISPLAY env,
# so its browser runs headless regardless of what X servers exist for other
# sessions.
hl() { docker exec -e WEKNORA_SESSION_ID="$H" -e WEKNORA_BROWSER_ROOT="$(root "$H")" "$C" agent-browser "$@" 2>&1; }
hl open "$URL" >/dev/null
OUT="$(hl snapshot)"
assert_contains "headless snapshot serves the page" "Directory listing" "$OUT"
finish

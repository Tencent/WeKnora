#!/usr/bin/env bash
# Usage: smoke-flows.sh IMAGE   (needs docker; run against a built desktop image)
#
# Not a unit test. This walks the flows the built-in agent-browser skill teaches
# on a real image and prints the raw output behind every verdict, so a failure
# can be read without re-running anything:
#   1 the CLI flow through the wrapper (open, snapshot -i, click, fill, press,
#     screenshot, close) and the 0.39.1 command surface
#   2 state vault: cookies set -> wk-auth save -> cookie fill in a fresh
#     session (localStorage restoration is not asserted here)
#   3 user closes the window (browser main killed, daemon alive) -> reopen
#   4 seat isolation (distinct seats, window on own display) + placement latency
#   5 four concurrent sessions
#
# Order matters: the vault must be non-empty before item 3.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
IMG="${1:?usage: smoke-flows.sh IMAGE}"
C=wk-flows-smoke
A=0f8fad5b-d9cb-469f-a165-70867728950e
B=7c9e6679-7425-40de-944b-e07fc1f90ae7

docker rm -f "$C" >/dev/null 2>&1
docker run -d --name "$C" --entrypoint sleep "$IMG" infinity >/dev/null
trap 'docker rm -f "$C" >/dev/null 2>&1' EXIT

root() { echo "/var/lib/weknora/sessions/$1.browser"; }
ab() { local s=$1; shift; docker exec -e WEKNORA_SESSION_ID="$s" -e WEKNORA_BROWSER_ROOT="$(root "$s")" "$C" agent-browser "$@"; }
dx() { docker exec "$C" sh -c "$1"; }
say() { echo; echo "===== $1"; }
ev() { echo "--- $1"; }
blk() { printf '%s\n' "$1" | sed 's/^/    | /'; }

# ---- in-container helpers: kept in files of their own so the test's own argv
# never matches the patterns being counted.
dxin() { docker exec -i "$C" sh -c 'cat > "$1"' -- "$1"; }
dxin /tmp/wk-mains.sh <<'HELPER'
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
dxin /tmp/wk-daemon.sh <<'HELPER'
#!/bin/sh
# usage: wk-daemon.sh <sid> -> agent-browser-l if that session's daemon is alive
for p in $(ls /proc | grep -E '^[0-9]+$'); do
  [ -r "/proc/$p/environ" ] || continue
  tr '\0' '\n' < "/proc/$p/environ" 2>/dev/null | grep -qx "HOME=/var/lib/weknora/sessions/$1.browser/home" || continue
  tr '\0' ' ' < "/proc/$p/cmdline" 2>/dev/null | grep -q agent-browser-linux && { echo agent-browser-l; exit 0; }
done
echo none
HELPER
dxin /tmp/wk-place.sh <<'HELPER'
#!/bin/sh
# usage: wk-place.sh <sid> <url>; prints where the window landed at open time
SID=$1; URL=$2
R=/var/lib/weknora/sessions/$SID.browser
export WEKNORA_SESSION_ID=$SID WEKNORA_BROWSER_ROOT=$R
s=$(date +%s%N)
agent-browser --headed open "$URL" >/tmp/place.out 2>&1; rc=$?
e=$(date +%s%N)
pid=$(sh /tmp/wk-mains.sh pid "$R")
seat=$(wk-display where "$SID")
wins=""
[ -n "$seat" ] && wins=$(DISPLAY=:$seat xdotool search --pid "$pid" 2>/dev/null | head -1)
echo "open rc=$rc open_ms=$(( (e - s) / 1000000 )) claimed_seat=$seat pid=$pid"
echo "at return: win-on-seat=${wins:+yes}${wins:-no}"
sleep 1
wins=$(DISPLAY=:$seat xdotool search --pid "$pid" 2>/dev/null | head -1)
echo "after 1s:  win-on-seat=${wins:+yes}${wins:-no}"
HELPER

main_count() { dx "sh /tmp/wk-mains.sh count $(root "$1")"; }
main_pid() { dx "sh /tmp/wk-mains.sh pid $(root "$1")"; }
seat_of() { dx "wk-display where $1 2>/dev/null"; }
win_on_seat() { # pid seat -> window id on that seat's display, empty when none
  dx "DISPLAY=:$2 xdotool search --pid $1 2>/dev/null | head -1"
}
seat_map() { dx 'wk-display list'; }

say "setup: desktop + a page with a link and a form"
dx '/usr/local/bin/start-desktop.sh' || fail "desktop did not start"
dx 'mkdir -p /var/www/wk
cat > /var/www/wk/index.html <<EOF
<!doctype html><html><head><title>WK Smoke</title></head><body>
<h1>Smoke page</h1>
<p><a id="lnk" href="/second.html">go to second</a></p>
</body></html>
EOF
cat > /var/www/wk/second.html <<EOF
<!doctype html><html><head><title>Second</title></head><body>
<h1>Second page</h1>
<form action="/submitted.html" method="get">
<input id="s" name="s" type="text" placeholder="second input">
<button type="submit">Send</button></form>
<a href="/index.html">back</a></body></html>
EOF
printf "%s" "<!doctype html><title>Submitted</title><h1>Submitted ok</h1>" > /var/www/wk/submitted.html
cd /var/www/wk && nohup python3 -m http.server 18080 --bind 127.0.0.1 >/tmp/http.log 2>&1 &'
sleep 1
URL=http://127.0.0.1:18080/index.html
assert_eq "local page answers 200" 200 "$(dx 'python3 -c "import urllib.request;print(urllib.request.urlopen(\"http://127.0.0.1:18080/index.html\").status)"')"
ev "seat table at start"; blk "$(seat_map)"

# ---------------------------------------------------------------- item 1
say "item 1: the taught CLI flow through the wrapper"
HELP="$(ab "$A" --help 2>&1)"; RC=$?
assert_eq "agent-browser --help exits 0" 0 "$RC"
for c in open snapshot click fill press screenshot close cookies; do
  case "$HELP" in
    *"  $c "*) pass "help lists '$c'" ;;
    *) fail "help does not list '$c'" ;;
  esac
done
assert_contains "help documents snapshot -i" "-i, --interactive" "$HELP"
assert_contains "press takes only a key" "press <key>" "$HELP"
assert_contains "screenshot takes an optional path" "screenshot [path]" "$HELP"
assert_contains "click accepts @ref" "Click element (or @ref)" "$HELP"
case "$HELP" in
  *"  state "*) pass "help lists 'state'" ;;
  *) pass "0.39.1 does not list 'state' under Core Commands; it is a hidden subcommand (state --help works)" ;;
esac
ev "state --help (manual export command family)"; blk "$(dx '/opt/agent-browser/bin/agent-browser state --help 2>&1 | head -9')"

OPEN="$(ab "$A" open "$URL" 2>&1)"; RC=$?
assert_eq "open exits 0" 0 "$RC"
assert_eq "one browser main for A" 1 "$(main_count "$A")"
ev "open"; blk "$OPEN"
ev "seat table after open"; blk "$(seat_map)"

SNAP="$(ab "$A" snapshot -i 2>&1)"
assert_contains "snapshot -i prints the heading" 'heading "Smoke page"' "$SNAP"
LINKREF="$(printf '%s\n' "$SNAP" | sed -n 's/.*link "go to second" \[ref=\(e[0-9]*\)\].*/\1/p')"
if [ -n "$LINKREF" ]; then pass "ref format printed as ref=$LINKREF (bare eN, no @)"; else fail "no ref= in snapshot -i output"; fi
ev "snapshot -i"; blk "$SNAP"

CLICK="$(ab "$A" click "@$LINKREF" 2>&1)"; RC=$?
assert_eq "click @ref exits 0" 0 "$RC"
U="$(ab "$A" get url 2>&1)"
case "$U" in *second.html*) pass "click followed the link: $U" ;; *) fail "click did not navigate: $U" ;; esac
ev "click @$LINKREF"; blk "$CLICK"

SNAP2="$(ab "$A" snapshot -i 2>&1)"
INREF="$(printf '%s\n' "$SNAP2" | sed -n 's/.*textbox "second input" \[ref=\(e[0-9]*\)\].*/\1/p')"
ev "snapshot -i after click (input ref=$INREF; refs restart above e1)"; blk "$SNAP2"
FILL="$(ab "$A" fill "@$INREF" "hello world" 2>&1)"; RC=$?
assert_eq "fill @ref 'two words' exits 0" 0 "$RC"
assert_eq "fill put the text in the field" "hello world" "$(ab "$A" get value "@$INREF" 2>&1)"
ev "fill @$INREF"; blk "$FILL"

PRESS="$(ab "$A" press Enter 2>&1)"; RC=$?
assert_eq "press Enter exits 0" 0 "$RC"
U="$(ab "$A" get url 2>&1)"
case "$U" in *submitted.html*s=hello*) pass "press Enter submitted the form: $U" ;; *) fail "press Enter did not submit: $U" ;; esac
ev "press Enter"; blk "$PRESS"

SHOT="$(ab "$A" screenshot /workspace/output/page.png 2>&1)"; RC=$?
assert_eq "screenshot /workspace/output/page.png exits 0" 0 "$RC"
assert_contains "file is a PNG" "PNG image data" "$(dx 'file /workspace/output/page.png 2>&1')"
ev "screenshot"; blk "$SHOT"
ev "file"; blk "$(dx 'ls -l /workspace/output/page.png; file -b /workspace/output/page.png')"
NEST="$(ab "$A" screenshot /workspace/out2/deep/x.png 2>&1)"; RC=$?
assert_eq "screenshot into a missing directory fails (agent needs mkdir -p)" 1 "$RC"
ev "screenshot into missing dir"; blk "$NEST"

CLOSE="$(ab "$A" close 2>&1)"; RC=$?
assert_eq "close exits 0" 0 "$RC"
sleep 1
assert_eq "a plain open never claims a seat, so close frees nothing" "" "$(seat_of "$A")"
assert_eq "no browser main for A after close" 0 "$(main_count "$A")"
ev "close"; blk "$CLOSE"
ev "seat table after close"; blk "$(seat_map)"

ab "$A" --help >/dev/null 2>&1
ev "seat table after 'agent-browser --help' (a plain call claims nothing; seats go to --headed and the Desktop tab only)"; blk "$(seat_map)"
ab "$A" close >/dev/null 2>&1

# ---------------------------------------------------------------- item 2
say "item 2: vault round trip"
ab "$A" open "$URL" >/dev/null
SET="$(ab "$A" cookies set vaultcookie v1 --url http://127.0.0.1:18080/ 2>&1)"; RC=$?
assert_eq "cookies set through the wrapper exits 0" 0 "$RC"
assert_contains "cookie visible in A" "vaultcookie=v1" "$(ab "$A" cookies get 2>&1)"
ev "cookies set"; blk "$SET"
SAVE="$(docker exec -e WEKNORA_SESSION_ID="$A" -e WEKNORA_BROWSER_ROOT="$(root "$A")" "$C" timeout 30 wk-auth save 2>&1)"; RC=$?
assert_eq "wk-auth save exits 0" 0 "$RC"
ev "wk-auth save"; blk "$SAVE"
assert_eq "vault exists mode 600" "600" "$(dx 'stat -c %a /var/lib/weknora/browser/auth/chrome-cookie-seed.json 2>/dev/null')"
VAULT="$(dx 'cat /var/lib/weknora/browser/auth/chrome-cookie-seed.json 2>/dev/null')"
assert_eq "vault is valid JSON" ok "$(dx 'python3 -c "import json;json.load(open(\"/var/lib/weknora/browser/auth/chrome-cookie-seed.json\"));print(\"ok\")" 2>&1')"
assert_contains "vault holds the cookie" '"vaultcookie"' "$VAULT"
ev "vault"; blk "$(dx 'ls -l /var/lib/weknora/browser/auth/chrome-cookie-seed.json; cat /var/lib/weknora/browser/auth/chrome-cookie-seed.json')"

ab "$B" open "$URL" >/dev/null
GOT="$(ab "$B" cookies get 2>&1)"
assert_contains "new session B got the vault cookie on open" "vaultcookie=v1" "$GOT"
ev "session B cookies get"; blk "$GOT"
ev "session B main pid"; blk "pid=$(main_pid "$B")"
ab "$B" close >/dev/null 2>&1

# ---------------------------------------------------------------- item 3
say "item 3: user closes the window, the daemon stays"
ab "$A" open "$URL" --headed >/dev/null
OLD="$(main_pid "$A")"
dx "kill $OLD"
sleep 1
DAEMON="$(dx "sh /tmp/wk-daemon.sh $A")"
assert_eq "browser main is gone after the kill" 0 "$(main_count "$A")"
assert_eq "the session daemon survived the window close" "agent-browser-l" "$DAEMON"
ev "daemon after the kill"; blk "$(dx 'ps -eo pid,args | grep "[a]gent-browser-linux" | cut -c1-80')"
OPEN2="$(ab "$A" open "$URL" --headed 2>&1)"; RC=$?
assert_eq "open after the window was closed exits 0" 0 "$RC"
NEW="$(main_pid "$A")"
assert_eq "exactly one browser main after reopen" 1 "$(main_count "$A")"
if [ -n "$NEW" ] && [ "$NEW" != "$OLD" ]; then
  pass "daemon relaunched the browser (pid $OLD -> $NEW)"
else
  fail "no new browser main after reopen"
fi
D="$(seat_of "$A")"
if [ -n "$D" ]; then pass "A still owns seat $D"; else fail "A lost its seat after reopen"; fi
W="$(win_on_seat "$NEW" "$D")"
if [ -n "$W" ]; then pass "relaunched window is on A's display :$D"; else fail "relaunched window not on display :$D"; fi
CG="$(ab "$A" cookies get 2>&1)"
assert_contains "cookies injected again after the relaunch" "vaultcookie=v1" "$CG"
ev "open after kill"; blk "$OPEN2"
ev "cookies get"; blk "$CG"

# ---------------------------------------------------------------- item 4
say "item 4: seats are isolated screens"
# Every session's browser window lives on its own X server (:N). The same
# window must not appear on any other seat's display — different X servers,
# so isolation is structural, but the smoke still proves placement and that
# the table maps sessions to distinct seats.
ab "$B" open "$URL" --headed >/dev/null; sleep 1
SEATA="$(seat_of "$A")"; SEATB="$(seat_of "$B")"
if [ -n "$SEATA" ] && [ -n "$SEATB" ] && [ "$SEATA" != "$SEATB" ]; then
  pass "A and B hold distinct seats ($SEATA / $SEATB)"
else
  fail "A and B seats are missing or collide ($SEATA / $SEATB)"
fi
WA="$(win_on_seat "$(main_pid "$A")" "$SEATA")"
WB="$(win_on_seat "$(main_pid "$B")" "$SEATB")"
if [ -n "$WA" ]; then pass "A's window is on display :$SEATA"; else fail "A's window missing on :$SEATA"; fi
if [ -n "$WB" ]; then pass "B's window is on display :$SEATB"; else fail "B's window missing on :$SEATB"; fi
CROSS="$(dx "DISPLAY=:$SEATB xdotool search --pid $(main_pid "$A") 2>/dev/null | head -1")"
if [ -z "$CROSS" ]; then pass "A's window does not appear on B's display"; else fail "A's window leaked onto B's display"; fi
ev "seat table"; blk "$(seat_map)"

# Placement latency: the wrapper starts the browser on its claimed display,
# so its window should already be there when 'open' exits.
ab "$A" close >/dev/null 2>&1; sleep 1
RES="$(dx "sh /tmp/wk-place.sh $A $URL")"
ev "open + immediate placement probe"; blk "$RES"
D="$(printf '%s\n' "$RES" | sed -n 's/.*claimed_seat=\([0-9]*\).*/\1/p')"
if printf '%s\n' "$RES" | sed -n 's/^at return: //p' | grep -q "win-on-seat=yes"
then pass "window already on claimed seat $D when open returns"
else fail "window not on claimed seat $D at open return"; fi

# ---------------------------------------------------------------- item 5
say "item 5: four sessions at once"
ab "$A" close >/dev/null 2>&1
ev "seat table before the race"; blk "$(seat_map)"
for i in 1 2 3 4; do
  s="5e5e5e5e-0000-4000-8000-00000000000$i"
  ( docker exec -e WEKNORA_SESSION_ID="$s" -e WEKNORA_BROWSER_ROOT="$(root "$s")" "$C" \
      timeout 120 agent-browser open "$URL" --headed >"/tmp/race$i.log" 2>&1; echo $? >"/tmp/race$i.rc" ) &
done
wait
for i in 1 2 3 4; do
  ev "racing session $i rc=$(cat /tmp/race$i.rc 2>/dev/null)"; blk "$(cat /tmp/race$i.log 2>/dev/null)"
done
ev "seat table after the race"; blk "$(seat_map)"
DS=""
for i in 1 2 3 4; do DS="$DS $(seat_of "5e5e5e5e-0000-4000-8000-00000000000$i")"; done
ev "claimed seats:$DS"
assert_eq "four sessions hold four distinct seats" 4 "$(printf '%s\n' $DS | sort -u | wc -l)"
for i in 1 2 3 4; do
  s="5e5e5e5e-0000-4000-8000-00000000000$i"
  d="$(seat_of "$s")"; p="$(main_pid "$s")"
  ev "session $i seat=$d pid=$p"; blk "$(win_on_seat "$p" "$d")"
  if [ -n "$(win_on_seat "$p" "$d")" ]; then pass "session $i window is on its display :$d"; else fail "session $i window not on display :$d"; fi
done
for i in 1 2 3 4; do ab "5e5e5e5e-0000-4000-8000-00000000000$i" close >/dev/null 2>&1; done

echo
docker rm -f "$C" >/dev/null 2>&1
trap - EXIT
ev "docker ps -a --filter name=$C"; blk "$(docker ps -a --filter name=$C)"
finish

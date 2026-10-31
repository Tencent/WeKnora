#!/usr/bin/env bash
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
WKCHROME="$HERE/../wk-chrome.sh"

setup_wk_chrome() {
  TMP="$(mktemp -d)"; export TMP
  mkdir -p "$TMP/bin"
  # A recording chrome-wk: proof we fell back to the plain shim.
  cat > "$TMP/bin/chrome-wk" <<'EOF'
#!/bin/sh
echo "chromewk $*" >> "$CHROME_WK_LOG"
EOF
  chmod 0755 "$TMP/bin/chrome-wk"
  # A recording wk-display: seat 3 has a holder, everything else does not.
  cat > "$TMP/bin/wk-display" <<'EOF'
#!/bin/sh
case "$1" in
  sid-of) case "$2" in
    3) echo "sess-3"; exit 0 ;;
    *) exit 1 ;;
  esac ;;
  *) exit 0 ;;
esac
EOF
  chmod 0755 "$TMP/bin/wk-display"
  # A recording agent-browser: proof we reached the session browser.
  cat > "$TMP/bin/agent-browser" <<'EOF'
#!/bin/sh
echo "agentbrowser sid=$WEKNORA_SESSION_ID root=$WEKNORA_BROWSER_ROOT args:$*" >> "$AB_LOG"
EOF
  chmod 0755 "$TMP/bin/agent-browser"
  export WK_CHROME_SHIM="$TMP/bin/chrome-wk" WK_DISPLAY="$TMP/bin/wk-display" \
         WK_AB_CMD="$TMP/bin/agent-browser"
  export CHROME_WK_LOG="$TMP/chrome-wk.log" AB_LOG="$TMP/ab.log"
  : > "$CHROME_WK_LOG"; : > "$AB_LOG"
  export WK_SESSIONS_ROOT="$TMP/sessions"
  # The session root the launcher expects exists only for sess-3.
  mkdir -p "$TMP/sessions/sess-3.browser"
}

t_a_seat_with_a_session_opens_the_session_browser() {
  setup_wk_chrome
  DISPLAY=:3 "$WKCHROME" https://x.example
  assert_contains "reached the session browser" "sid=sess-3" "$(cat "$AB_LOG")"
  assert_contains "url forwarded" "https://x.example" "$(cat "$AB_LOG")"
  assert_contains "headed so the window lands on the seat" "--headed" "$(cat "$AB_LOG")"
  teardown
}

t_no_seat_falls_back_to_chrome_wk() {
  setup_wk_chrome
  DISPLAY= "$WKCHROME" https://x.example
  assert_contains "fell back to the plain shim" "chromewk" "$(cat "$CHROME_WK_LOG")"
  assert_eq "session path untouched" "" "$(cat "$AB_LOG")"
  teardown
}

t_seat_zero_falls_back_to_chrome_wk() {
  setup_wk_chrome
  DISPLAY=:0 "$WKCHROME"
  assert_contains "legacy shared desktop falls back" "chromewk" "$(cat "$CHROME_WK_LOG")"
  teardown
}

t_free_seat_falls_back_to_chrome_wk() {
  setup_wk_chrome
  DISPLAY=:4 "$WKCHROME"   # the stub wk-display reports no holder for 4
  assert_contains "unheld seat falls back" "chromewk" "$(cat "$CHROME_WK_LOG")"
  teardown
}

t_a_seat_before_any_agent_browser_still_opens_the_session_browser() {
  setup_wk_chrome
  rm -rf "$WK_SESSIONS_ROOT/sess-3.browser"
  DISPLAY=:3 "$WKCHROME" https://x.example
  assert_eq "no plain Chrome" "" "$(cat "$CHROME_WK_LOG")"
  assert_contains "the shared-machine root of the session" \
    "root=$WK_SESSIONS_ROOT/sess-3.browser" "$(cat "$AB_LOG")"
  teardown
}

t_a_single_session_machine_uses_the_single_session_root() {
  setup_wk_chrome
  rm -rf "$WK_SESSIONS_ROOT"
  export WK_SINGLE_BROWSER_ROOT="$TMP/browser/session"
  DISPLAY=:3 "$WKCHROME"
  assert_eq "no plain Chrome" "" "$(cat "$CHROME_WK_LOG")"
  assert_contains "the root the agent uses on this machine" \
    "root=$TMP/browser/session" "$(cat "$AB_LOG")"
  unset WK_SINGLE_BROWSER_ROOT
  teardown
}

t_an_unsafe_session_id_falls_back_to_chrome_wk() {
  setup_wk_chrome
  cat > "$TMP/bin/wk-display" <<'EOF'
#!/bin/sh
echo "../etc"
EOF
  DISPLAY=:3 "$WKCHROME"
  assert_contains "no path outside the sessions root" "chromewk" "$(cat "$CHROME_WK_LOG")"
  assert_eq "session path untouched" "" "$(cat "$AB_LOG")"
  teardown
}

t_a_seat_with_a_session_opens_the_session_browser
t_no_seat_falls_back_to_chrome_wk
t_seat_zero_falls_back_to_chrome_wk
t_free_seat_falls_back_to_chrome_wk
t_a_seat_before_any_agent_browser_still_opens_the_session_browser
t_a_single_session_machine_uses_the_single_session_root
t_an_unsafe_session_id_falls_back_to_chrome_wk
finish

#!/usr/bin/env bash
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
WRAP="$HERE/../agent-browser-wrapper.sh"
DISP="$HERE/stubs/wk-display-stub.sh"

setup_wrapper() {
  TMP="$(mktemp -d)"; export TMP
  # Stub only the sync service; exercise the production navigation handshake.
  export STUB_STATE="$TMP/stub" STUB_LOG="$TMP/stub.log"
  mkdir -p "$STUB_STATE"; : > "$STUB_LOG"
  export WK_DISPLAY_STATE="$TMP/display" WK_DISPLAY_LOG="$TMP/display.log"
  mkdir -p "$WK_DISPLAY_STATE"; : > "$WK_DISPLAY_LOG"
  export WEKNORA_SESSION_ID="sid-A" WEKNORA_BROWSER_ROOT="$TMP/br/A"
  export WK_AB_REAL="$HERE/stubs/agent-browser-stub.sh" WK_DISPLAY="$DISP"
  # The sync stub acknowledges navigation requests and records close captures.
  cat > "$TMP/fake-sync" <<'EOF'
#!/usr/bin/env bash
echo "sync $*" >> "$WK_SYNC_LOG"
case "$*" in *--capture*) exit "${STUB_SYNC_CAPTURE_RC:-0}" ;; esac
exit 0
EOF
  chmod 0755 "$TMP/fake-sync"
  export WK_SYNC_CMD="$TMP/fake-sync" WK_SYNC_LOG="$TMP/sync.log"
  unset STUB_SYNC_CAPTURE_RC
  : > "$WK_SYNC_LOG"
  export WK_CHROME="/usr/local/bin/chrome-wk"
  unset STUB_OPEN_RC STUB_FAIL STUB_BAD_JSON WK_DISPLAY_MAX
}
log() { cat "$STUB_LOG"; }
dlog() { cat "$WK_DISPLAY_LOG"; }
t_fails_closed_without_session_env() {
  setup_wrapper
  env -u WEKNORA_SESSION_ID "$WRAP" snapshot 2>/dev/null; rc=$?
  assert_eq "exit 70" 70 "$rc"
  assert_eq "real binary never ran" "" "$(log)"
  teardown
}

t_pins_home_profile_session_and_executable() {
  setup_wrapper
  "$WRAP" snapshot
  assert_contains "HOME is the session's" "HOME=$WEKNORA_BROWSER_ROOT/home" "$(log)"
  assert_contains "fixed session name" "SESSION=main" "$(log)"
  assert_contains "profile pinned" "--profile $WEKNORA_BROWSER_ROOT/profile" "$(log)"
  assert_contains "chrome shim pinned" "EXE=/usr/local/bin/chrome-wk" "$(log)"
  assert_not_contains "no --headed unless asked" "--headed" "$(log)"
  assert_contains "headless leaves DISPLAY empty" "DISPLAY= " "$(log)"
  assert_eq "headless does not claim a seat" "" "$(dlog)"
  teardown
}

t_two_sessions_get_different_displays() {
  setup_wrapper
  "$WRAP" --headed snapshot
  WEKNORA_SESSION_ID=sid-B WEKNORA_BROWSER_ROOT="$TMP/br/B" "$WRAP" --headed snapshot
  assert_contains "A home" "HOME=$TMP/br/A/home" "$(log)"
  assert_contains "B home" "HOME=$TMP/br/B/home" "$(log)"
  assert_contains "A on seat 1" "DISPLAY=:1 " "$(log)"
  assert_contains "B on seat 2" "DISPLAY=:2 " "$(log)"
  teardown
}

t_repeat_calls_keep_the_same_display() {
  setup_wrapper
  "$WRAP" --headed snapshot
  "$WRAP" --headed snapshot
  assert_eq "both calls on seat 1" 2 "$(log | grep -c 'DISPLAY=:1 ')"
  assert_eq "no second seat" "" "$(cat "$WK_DISPLAY_STATE/seats/2" 2>/dev/null)"
  teardown
}

t_replaces_flags_that_would_escape_isolation() {
  setup_wrapper
  "$WRAP" --cdp 9222 --session x --profile /tmp/p --state s.json \
    --executable-path /bin/x --auto-connect open https://x; rc=$?
  assert_eq "command still runs" 0 "$rc"
  assert_contains "our profile" "--profile $WEKNORA_BROWSER_ROOT/profile" "$(log)"
  assert_contains "our session" "SESSION=main" "$(log)"
  assert_contains "our chrome" "EXE=/usr/local/bin/chrome-wk" "$(log)"
  assert_not_contains "cdp dropped" "9222" "$(log)"
  assert_not_contains "foreign profile dropped" "/tmp/p" "$(log)"
  assert_not_contains "state file dropped" "s.json" "$(log)"
  assert_not_contains "executable dropped" "/bin/x" "$(log)"
  assert_not_contains "auto-connect dropped" "auto-connect" "$(log)"
  assert_not_contains "foreign session dropped" "--session" "$(log)"
  : > "$STUB_LOG"
  "$WRAP" --engine chrome --idle-timeout 5000 open https://y
  assert_contains "value-taking global flags pass through" "--engine chrome" "$(log)"
  assert_contains "navigation still detected after them" " open https://y" "$(log)"
  assert_contains "idle timeout passes" "--idle-timeout 5000" "$(log)"
  : > "$STUB_LOG"
  "$WRAP" -p browserbase --provider browserless --provider=agentcore open https://z; rc=$?
  assert_eq "provider flags dropped, command still runs" 0 "$rc"
  assert_not_contains "short provider dropped" "browserbase" "$(log)"
  assert_not_contains "long provider dropped" "browserless" "$(log)"
  assert_not_contains "equals provider dropped" "agentcore" "$(log)"
  assert_contains "url kept after provider drops" " open https://z" "$(log)"
  : > "$STUB_LOG"
  "$WRAP" open --profile=/tmp/other --cdp=9222 https://y; rc=$?
  assert_eq "flags after the subcommand are dropped too" 0 "$rc"
  assert_not_contains "equals-form profile dropped" "/tmp/other" "$(log)"
  assert_not_contains "equals-form cdp dropped" "9222" "$(log)"
  assert_contains "url kept" "open https://y" "$(log)"
  teardown
}

t_headed_boolean_value_forms() {
  setup_wrapper
  "$WRAP" --headed false open https://example.com
  assert_contains "explicit disable forwarded verbatim" "--headed false open" "$(log)"
  assert_eq "no seat claimed for --headed false" "" "$(dlog)"
  assert_contains "headless display for --headed false" "DISPLAY= " "$(log)"
  : > "$STUB_LOG"; : > "$WK_DISPLAY_LOG"
  "$WRAP" --headed=false snapshot
  assert_contains "equals-false forwarded" "--headed=false" "$(log)"
  assert_eq "equals-false claims nothing" "" "$(dlog)"
  : > "$STUB_LOG"; : > "$WK_DISPLAY_LOG"
  "$WRAP" --headed=true snapshot
  assert_contains "equals-true forwarded" "--headed=true" "$(log)"
  assert_contains "equals-true claims the seat" "claim sid-A" "$(dlog)"
  : > "$STUB_LOG"; : > "$WK_DISPLAY_LOG"
  "$WRAP" --headed true open https://example.com
  assert_contains "explicit enable forwarded verbatim" "--headed true open" "$(log)"
  assert_contains "true still claims the seat" "claim sid-A" "$(dlog)"
  teardown
}

t_close_keeps_the_seat() {
  setup_wrapper
  "$WRAP" --headed open https://example.com
  "$WRAP" --json close; rc=$?
  assert_eq "close ok" 0 "$rc"
  assert_eq "seat still belongs to the session" sid-A "$(cat "$WK_DISPLAY_STATE/seats/1")"
  assert_not_contains "close does not release" "release" "$(dlog)"
  teardown
}

t_close_without_a_browser_passes_the_exit_code_and_claims_nothing() {
  setup_wrapper
  "$WRAP" close; rc=$?
  assert_eq "real exit code surfaces" 1 "$rc"
  assert_not_contains "close does not claim" "claim" "$(dlog)"
  assert_eq "no seat allocated" "" "$(ls -A "$WK_DISPLAY_STATE/seats" 2>/dev/null)"
  teardown
}

t_nonzero_open_still_exits_with_its_code() {
  setup_wrapper
  STUB_OPEN_RC=3 "$WRAP" open https://example.com; rc=$?
  assert_eq "no set -e: code passes through" 3 "$rc"
  teardown
}

t_navigation_waits_for_sync() {
  setup_wrapper
  "$WRAP" open https://example.com
  assert_eq "first launch bootstraps one blank page" 1 "$(log | grep -c ' open about:blank')"
  assert_eq "requested URL opens once" 1 "$(log | grep -c ' open https://example.com')"
  assert_contains "cold start restores targeted browser" "--restore-root $WEKNORA_BROWSER_ROOT" "$(cat "$WK_SYNC_LOG")"
  "$WRAP" open https://example.com/next
  assert_eq "existing browser does not revisit blank page" 1 "$(log | grep -c ' open about:blank')"
  assert_not_contains "CLI state loading is unused" "state load" "$(log)"
  assert_not_contains "CLI storage injection is unused" "storage local" "$(log)"
  assert_not_contains "caller state path is never used" "--state" "$(log)"
  teardown
}

t_missing_sync_blocks_navigation() {
  setup_wrapper
  WK_SYNC_CMD="$TMP/missing-sync" "$WRAP" open https://example.com >/dev/null 2>&1; rc=$?
  assert_eq "missing sync exits 70" 70 "$rc"
  assert_not_contains "target navigation is deferred" "open https://example.com" "$(log)"
  teardown
}

t_full_machine_degrades_instead_of_failing() {
  setup_wrapper
  export WK_DISPLAY_MAX=1
  mkdir -p "$WK_DISPLAY_STATE/seats"
  echo other > "$WK_DISPLAY_STATE/seats/1"
  err="$("$WRAP" --headed open https://example.com 2>&1 >/dev/null)"; rc=$?
  assert_eq "command still succeeds" 0 "$rc"
  assert_contains "warns that the screen is full" "no free screen" "$err"
  assert_contains "falls back to headless" "DISPLAY= " "$(log)"
  assert_not_contains "headless run is not headed" "--headed" "$(log)"
  teardown
}

t_sessions_share_login_files_at_startup() {
  setup_wrapper
  export WK_LINK_CHROME="$HERE/../link-chrome-session.sh" WK_CHROME_SHARED="$TMP/shared"
  "$WRAP" open https://example.com
  WEKNORA_SESSION_ID=sid-B WEKNORA_BROWSER_ROOT="$TMP/br/B" "$WRAP" open https://example.com
  for name in 'Login Data' 'Login Data For Account'; do
    a="$TMP/br/A/profile/Default/$name"
    b="$TMP/br/B/profile/Default/$name"
    assert_eq "$name shared by A" "$TMP/shared/Default/$name" "$(readlink "$a")"
    assert_eq "$name shared by B" "$(readlink "$a")" "$(readlink "$b")"
  done
  assert_eq "Cookies stay private" "" "$(readlink "$TMP/br/A/profile/Default/Cookies")"
  unset WK_LINK_CHROME WK_CHROME_SHARED
  teardown
}

t_headed_flag_claims_the_session_screen() {
  setup_wrapper
  "$WRAP" --headed open https://example.com
  assert_contains "caller flag is kept" "--headed open" "$(log)"
  assert_contains "window goes to the claimed seat" "DISPLAY=:1 " "$(log)"
  assert_contains "process is tagged with the seat" "SEAT=1" "$(log)"
  teardown
}

t_fails_closed_without_session_env
t_pins_home_profile_session_and_executable
t_two_sessions_get_different_displays
t_repeat_calls_keep_the_same_display
t_replaces_flags_that_would_escape_isolation
t_headed_boolean_value_forms
t_close_keeps_the_seat
t_close_without_a_browser_passes_the_exit_code_and_claims_nothing
t_nonzero_open_still_exits_with_its_code
t_navigation_waits_for_sync
t_missing_sync_blocks_navigation
t_full_machine_degrades_instead_of_failing
t_sessions_share_login_files_at_startup
t_headed_flag_claims_the_session_screen

t_close_harvests_cookies_into_the_vault() {
  setup_wrapper
  "$WRAP" open http://a.example >/dev/null 2>&1
  assert_not_contains "no harvest on a non-close command" "--capture" "$(cat "$WK_SYNC_LOG")"
  "$WRAP" close >/dev/null 2>&1
  assert_contains "close captures via sync service" "--capture $WEKNORA_BROWSER_ROOT" "$(cat "$WK_SYNC_LOG")"
  # Capture must precede the final CLI call while Chrome is still alive.
  ab_line=$(grep -n '^ab "' "$WRAP" | head -1 | cut -d: -f1)
  harvest_line=$(grep -n 'timeout 10' "$WRAP" | head -1 | cut -d: -f1)
  [ -n "$ab_line" ] && [ -n "$harvest_line" ] && [ "$harvest_line" -lt "$ab_line" ] \
    && pass "harvest runs before the close call" || fail "harvest order wrong"
  teardown
}

t_a_failed_harvest_does_not_break_close() {
  setup_wrapper
  "$WRAP" open http://a.example >/dev/null 2>&1
  rc=0; STUB_SYNC_CAPTURE_RC=1 "$WRAP" close >/dev/null 2>&1 || rc=$?
  assert_eq "close still exits 0 when the harvest fails" 0 "$rc"
  teardown
}

t_close_harvests_cookies_into_the_vault
t_a_failed_harvest_does_not_break_close
finish

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
  teardown
}

t_a_browser_launch_defaults_to_headed() {
  setup_wrapper
  "$WRAP" open https://example.com
  assert_contains "the seat is claimed without --headed" "claim sid-A" "$(dlog)"
  assert_contains "the launch is headed" "--headed open about:blank" "$(log)"
  assert_contains "window goes to the claimed seat" "DISPLAY=:1 " "$(log)"
  teardown
}

t_explicit_headless_is_honored() {
  setup_wrapper
  "$WRAP" --headed false open https://example.com
  assert_eq "no seat claimed" "" "$(dlog)"
  assert_contains "headless leaves DISPLAY empty" "DISPLAY= " "$(log)"
  assert_not_contains "no headed launch" "--headed open" "$(log)"
  teardown
}

t_commands_without_a_browser_claim_nothing() {
  setup_wrapper
  "$WRAP" --help
  "$WRAP" skills get core
  "$WRAP" session list
  "$WRAP" close
  assert_eq "no seat claimed" "" "$(dlog)"
  assert_not_contains "no --headed added" "--headed" "$(log)"
  teardown
}

t_a_running_browser_is_not_relaunched_headed() {
  setup_wrapper
  "$WRAP" --headed false open https://example.com
  : > "$STUB_LOG"
  "$WRAP" snapshot
  assert_eq "no seat claimed for a running browser" "" "$(dlog)"
  assert_not_contains "no --headed added" "--headed" "$(log)"
  teardown
}

t_desktop_click_with_a_url_opens_a_new_tab() {
  setup_wrapper
  "$WRAP" open https://agent.example
  : > "$STUB_LOG"
  WK_DESKTOP_CLICK=1 "$WRAP" --headed open https://human.example
  assert_contains "the URL gets its own tab" "tab new https://human.example" "$(log)"
  assert_not_contains "the agent's page is not navigated" "open https://human.example" "$(log)"
  teardown
}

t_desktop_click_without_a_url_keeps_the_page() {
  setup_wrapper
  cat > "$TMP/fake-xdotool" <<'EOF'
#!/bin/sh
echo "xdotool DISPLAY=$DISPLAY $*" >> "$WK_SYNC_LOG"
EOF
  chmod 0755 "$TMP/fake-xdotool"
  export WK_XDOTOOL="$TMP/fake-xdotool"
  "$WRAP" open https://agent.example
  : > "$STUB_LOG"
  WK_DESKTOP_CLICK=1 "$WRAP" --headed open about:blank; rc=$?
  assert_eq "click succeeds" 0 "$rc"
  assert_eq "the browser gets no command" "" "$(grep '^ARGS' "$STUB_LOG")"
  assert_contains "the window is raised on the seat" "xdotool DISPLAY=:1 " "$(cat "$WK_SYNC_LOG")"
  unset WK_XDOTOOL
  teardown
}

t_desktop_click_on_a_cold_start_opens_the_browser() {
  setup_wrapper
  WK_DESKTOP_CLICK=1 "$WRAP" --headed open https://human.example
  assert_contains "cold start boots the browser" "--headed open about:blank" "$(log)"
  assert_contains "then opens the URL" "open https://human.example" "$(log)"
  assert_not_contains "no tab on a fresh browser" "tab new" "$(log)"
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
  : > "$STUB_LOG"
  "$WRAP" close >/dev/null 2>&1
  err="$("$WRAP" open https://example.com 2>&1 >/dev/null)"; rc=$?
  assert_eq "default-headed launch still succeeds" 0 "$rc"
  assert_contains "default-headed launch falls back to headless" "DISPLAY= " "$(log)"
  assert_not_contains "the implicit --headed is dropped too" "--headed" "$(log)"
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

t_desktop_click_and_agent_reach_the_same_daemon() {
  setup_wrapper
  # wk-chrome runs under a seat's XDG_RUNTIME_DIR; the agent's exec has none.
  XDG_RUNTIME_DIR="$TMP/seat-1/xdg" "$WRAP" --headed open https://human.example
  env -u XDG_RUNTIME_DIR "$WRAP" open https://agent.example
  assert_eq "one daemon socket directory for both callers" 1 \
    "$(log | grep '^SOCKDIR ' | sort -u | wc -l | tr -d ' ')"
  assert_contains "socket lives under the session root" \
    "SOCKDIR $WEKNORA_BROWSER_ROOT/home/.agent-browser" "$(log)"
  teardown
}

t_lock_held_skips_the_command_lock() {
  setup_wrapper
  mkdir -p "$WEKNORA_BROWSER_ROOT"
  exec 9>"$WEKNORA_BROWSER_ROOT/.command.lock"
  flock 9
  WK_AB_LOCK_HELD=1 timeout 5 "$WRAP" focus @e1; rc=$?
  exec 9>&-
  assert_eq "runs while the secret-fill helper holds the lock" 0 "$rc"
  assert_contains "the command reached the browser" "ARGS" "$(log)"
  teardown
}

t_eval_reading_a_password_value_is_refused() {
  setup_wrapper
  "$WRAP" eval 'document.querySelector("input[type=Password]").value' 2>"$TMP/err"; rc=$?
  assert_eq "refused with a usage error" 64 "$rc"
  assert_contains "explains why" "browser_fill_secret" "$(cat "$TMP/err")"
  assert_not_contains "never reached the browser" "ARGS eval" "$(log)"
  "$WRAP" --headed eval 'document.querySelector("input[type=Password]").value' 2>/dev/null; rc=$?
  assert_eq "refused even with leading flags" 64 "$rc"
  "$WRAP" eval 'document.title'; rc=$?
  assert_eq "other scripts still run" 0 "$rc"
  teardown
}

t_fails_closed_without_session_env
t_desktop_click_and_agent_reach_the_same_daemon
t_pins_home_profile_session_and_executable
t_a_browser_launch_defaults_to_headed
t_explicit_headless_is_honored
t_commands_without_a_browser_claim_nothing
t_a_running_browser_is_not_relaunched_headed
t_desktop_click_with_a_url_opens_a_new_tab
t_desktop_click_without_a_url_keeps_the_page
t_desktop_click_on_a_cold_start_opens_the_browser
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

t_caller_config_cannot_reach_the_cli() {
  setup_wrapper
  mkdir -p "$TMP/ws"
  echo '{"namespace":"from-cwd"}' > "$TMP/ws/agent-browser.json"
  (cd "$TMP/ws" && AGENT_BROWSER_NAMESPACE=envns AGENT_BROWSER_CDP=9222 AGENT_BROWSER_JSON=1 \
    "$WRAP" --namespace flagns --config /tmp/foreign.json \
    --args "--remote-debugging-address=0.0.0.0" --extension /tmp/ext \
    --init-script=/tmp/init.js snapshot); rc=$?
  assert_eq "command still runs" 0 "$rc"
  assert_contains "the command reaches the CLI" " snapshot" "$(log)"
  assert_not_contains "env namespace dropped" "envns" "$(log)"
  assert_not_contains "env cdp dropped" "9222" "$(log)"
  assert_not_contains "flag namespace dropped" "flagns" "$(log)"
  assert_not_contains "foreign config dropped" "/tmp/foreign.json" "$(log)"
  assert_not_contains "launch args dropped" "0.0.0.0" "$(log)"
  assert_not_contains "extension dropped" "/tmp/ext" "$(log)"
  assert_not_contains "init script dropped" "/tmp/init.js" "$(log)"
  assert_contains "harmless output switches pass" "AGENT_BROWSER_JSON=1" "$(log)"
  cfg="$WEKNORA_BROWSER_ROOT/home/agent-browser-config.json"
  assert_contains "config pinned to the wrapper's own file" "AGENT_BROWSER_CONFIG=$cfg" "$(log)"
  assert_eq "the pinned config is empty" "{}" "$(cat "$cfg")"
  teardown
}

t_connect_is_refused() {
  setup_wrapper
  err="$("$WRAP" connect 9222 2>&1)"; rc=$?
  assert_eq "connect exits 64" 64 "$rc"
  assert_contains "explains why" "connect" "$err"
  assert_eq "the CLI never ran" "" "$(grep '^ARGS' "$STUB_LOG")"
  teardown
}

record_group() { # a fake daemon that records whether it leads its own process group
  cat > "$TMP/$1" <<EOF
#!/bin/bash
echo "\$\$ \$(ps -o pgid= -p \$\$ | tr -d ' ')" >> "$TMP/$1.log"
case "\$*" in *--*) exit 0 ;; esac
sleep 5
EOF
  chmod 0755 "$TMP/$1"
}

t_machine_daemons_leave_the_command_process_group() {
  setup_wrapper
  record_group gov; record_group syncd
  export WK_GOVERNOR_CMD="$TMP/gov" WK_SYNC_CMD="$TMP/syncd" WK_GOVERNOR_WAIT_S=0
  "$WRAP" open https://example.com >/dev/null 2>&1
  sleep 0.5
  read -r pid pgid < "$TMP/gov.log"
  assert_eq "governor leads its own process group" "$pid" "$pgid"
  read -r pid pgid < <(tail -1 "$TMP/syncd.log")
  assert_eq "sync daemon leads its own process group" "$pid" "$pgid"
  unset WK_GOVERNOR_CMD WK_GOVERNOR_WAIT_S
  teardown
}

t_navigation_to_a_running_browser_restarts_the_governor() {
  setup_wrapper
  record_group gov
  export WK_GOVERNOR_CMD="$TMP/gov" WK_GOVERNOR_WAIT_S=0
  "$WRAP" open https://example.com >/dev/null 2>&1
  : > "$TMP/gov.log"
  "$WRAP" open https://example.com/next >/dev/null 2>&1
  sleep 0.5
  assert_eq "governor started again for a running browser" 1 "$(wc -l < "$TMP/gov.log" | tr -d ' ')"
  unset WK_GOVERNOR_CMD WK_GOVERNOR_WAIT_S
  teardown
}

t_a_watched_browser_is_kept_alive() {
  setup_wrapper
  export WK_KEEPALIVE_S=0.2
  touch "$WK_DISPLAY_STATE/watched"
  "$WRAP" open https://example.com >/dev/null 2>&1
  sleep 1.5
  assert_contains "a watched browser gets keepalive commands" " get url" "$(log)"
  "$WRAP" close >/dev/null 2>&1
  sleep 0.6
  n=$(grep -c ' get url' "$STUB_LOG")
  sleep 1
  assert_eq "keepalive stops with the browser" "$n" "$(grep -c ' get url' "$STUB_LOG")"
  unset WK_KEEPALIVE_S
  teardown
}

t_an_unwatched_browser_gets_no_keepalive() {
  setup_wrapper
  export WK_KEEPALIVE_S=0.2
  "$WRAP" open https://example.com >/dev/null 2>&1
  sleep 1.5
  assert_not_contains "no keepalive without a viewer" " get url" "$(log)"
  "$WRAP" close >/dev/null 2>&1
  unset WK_KEEPALIVE_S
  teardown
}

t_agent_navigation_after_a_desktop_click_takes_a_new_tab() {
  setup_wrapper
  WK_DESKTOP_CLICK=1 "$WRAP" --headed open https://human.example >/dev/null 2>&1
  : > "$STUB_LOG"
  "$WRAP" open https://agent.example
  assert_contains "the agent gets a tab of its own" "tab new https://agent.example" "$(log)"
  assert_not_contains "the human's page is not navigated" "open https://agent.example" "$(log)"
  : > "$STUB_LOG"
  "$WRAP" open https://agent.example/next
  assert_contains "later navigation stays in the agent's tab" " open https://agent.example/next" "$(log)"
  assert_not_contains "only one new tab" "tab new" "$(log)"
  teardown
}

t_a_fresh_browser_forgets_an_old_desktop_click() {
  setup_wrapper
  WK_DESKTOP_CLICK=1 "$WRAP" --headed open https://human.example >/dev/null 2>&1
  "$WRAP" close >/dev/null 2>&1
  "$WRAP" open https://agent.example >/dev/null 2>&1
  : > "$STUB_LOG"
  "$WRAP" open https://agent.example/next
  assert_not_contains "no stale new tab" "tab new" "$(log)"
  teardown
}

t_an_orphaned_browser_is_replaced() {
  setup_wrapper
  "$WRAP" open https://example.com >/dev/null 2>&1
  old="$(cat "$STUB_STATE/chrome.pid")"
  rm -f "$WEKNORA_BROWSER_ROOT/home/.agent-browser/main.pid"   # its daemon died
  : > "$WK_SYNC_LOG"
  err="$("$WRAP" snapshot 2>&1 >/dev/null)"; rc=$?
  assert_eq "the command still runs" 0 "$rc"
  assert_eq "the orphaned browser is gone" 1 "$(kill -0 "$old" 2>/dev/null; echo $?)"
  assert_contains "its login state is captured first" "--capture $WEKNORA_BROWSER_ROOT" "$(cat "$WK_SYNC_LOG")"
  assert_contains "says what happened" "controller" "$err"
  assert_contains "the relaunch is headed" "--headed snapshot" "$(log)"
  teardown
}

t_a_browser_with_a_live_daemon_is_kept() {
  setup_wrapper
  "$WRAP" open https://example.com >/dev/null 2>&1
  pid="$(cat "$STUB_STATE/chrome.pid")"
  "$WRAP" snapshot >/dev/null 2>&1
  assert_eq "the browser survives" 0 "$(kill -0 "$pid" 2>/dev/null; echo $?)"
  teardown
}

t_a_busy_command_lock_is_explained() {
  setup_wrapper
  mkdir -p "$WEKNORA_BROWSER_ROOT"
  ( exec 7>"$WEKNORA_BROWSER_ROOT/.command.lock"; flock 7; sleep 3 ) &
  holder=$!
  sleep 0.3
  err="$(WK_LOCK_WAIT_S=1 "$WRAP" snapshot 2>&1 >/dev/null)"; rc=$?
  assert_eq "exit 70" 70 "$rc"
  assert_contains "says another command is running" "still running" "$err"
  kill "$holder" 2>/dev/null
  teardown
}

setup_cgroup() {
  export WK_CGROUP_DIR="$TMP/cg"
  mkdir -p "$WK_CGROUP_DIR"
  echo "max 2" > "$WK_CGROUP_DIR/pids.events"
  echo 512 > "$WK_CGROUP_DIR/pids.max"; echo 509 > "$WK_CGROUP_DIR/pids.current"
  printf 'low 0\nhigh 0\nmax 0\noom 0\noom_kill 1\n' > "$WK_CGROUP_DIR/memory.events"
  echo 2147483648 > "$WK_CGROUP_DIR/memory.max"; echo 2100000000 > "$WK_CGROUP_DIR/memory.current"
}

t_a_failure_at_the_process_limit_says_so() {
  setup_wrapper; setup_cgroup
  err="$(STUB_OPEN_RC=1 STUB_ON_OPEN="echo 'max 9' > $WK_CGROUP_DIR/pids.events" \
    "$WRAP" open https://example.com 2>&1 >/dev/null)"; rc=$?
  assert_eq "the CLI's code passes through" 1 "$rc"
  assert_contains "names the process limit" "process limit" "$err"
  assert_contains "shows usage against the limit" "509/512" "$err"
  unset WK_CGROUP_DIR
  teardown
}

t_a_failure_after_an_oom_kill_says_so() {
  setup_wrapper; setup_cgroup
  err="$(STUB_OPEN_RC=1 STUB_ON_OPEN="printf 'oom_kill 3\n' > $WK_CGROUP_DIR/memory.events" \
    "$WRAP" open https://example.com 2>&1 >/dev/null)"; rc=$?
  assert_eq "the CLI's code passes through" 1 "$rc"
  assert_contains "names the memory shortage" "out of memory" "$err"
  assert_contains "shows the memory limit" "2048MiB" "$err"
  unset WK_CGROUP_DIR
  teardown
}

t_an_ordinary_failure_blames_no_resource() {
  setup_wrapper; setup_cgroup
  err="$(STUB_OPEN_RC=3 "$WRAP" open https://example.com 2>&1 >/dev/null)"; rc=$?
  assert_eq "the CLI's code passes through" 3 "$rc"
  assert_not_contains "no process-limit claim" "process limit" "$err"
  assert_not_contains "no memory claim" "out of memory" "$err"
  unset WK_CGROUP_DIR
  teardown
}

t_a_failure_without_cgroup_files_is_unchanged() {
  setup_wrapper
  export WK_CGROUP_DIR="$TMP/no-cgroup"
  err="$(STUB_OPEN_RC=1 "$WRAP" open https://example.com 2>&1 >/dev/null)"; rc=$?
  assert_eq "the CLI's code passes through" 1 "$rc"
  assert_not_contains "nothing to report" "limit" "$err"
  unset WK_CGROUP_DIR
  teardown
}

t_caller_config_cannot_reach_the_cli
t_a_failure_at_the_process_limit_says_so
t_a_failure_after_an_oom_kill_says_so
t_an_ordinary_failure_blames_no_resource
t_a_failure_without_cgroup_files_is_unchanged
t_agent_navigation_after_a_desktop_click_takes_a_new_tab
t_a_fresh_browser_forgets_an_old_desktop_click
t_an_orphaned_browser_is_replaced
t_a_browser_with_a_live_daemon_is_kept
t_a_busy_command_lock_is_explained
t_connect_is_refused
t_machine_daemons_leave_the_command_process_group
t_navigation_to_a_running_browser_restarts_the_governor
t_a_watched_browser_is_kept_alive
t_an_unwatched_browser_gets_no_keepalive
t_lock_held_skips_the_command_lock
t_eval_reading_a_password_value_is_refused
finish

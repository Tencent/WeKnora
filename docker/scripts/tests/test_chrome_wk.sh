#!/usr/bin/env bash
# chrome-wk argument shaping. The real browser is replaced so exec is the
# recorded command and the script's pid is the stub's pid.
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
CHROME="$HERE/../chrome-wk.sh"

setup_chrome() {
  TMP="$(mktemp -d)"; export TMP
  cat > "$TMP/chrome" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" > "$WK_CHROME_ARGS"
printf '%s\n' "$$" > "$WK_CHROME_PID"
EOF
  chmod 0755 "$TMP/chrome"
  export WK_CHROME_REAL="$TMP/chrome" WK_CHROME_ARGS="$TMP/args" WK_CHROME_PID="$TMP/pid"
  unset WK_SEAT WK_SCREEN WK_MACHINE_ID_SCRIPT
}

t_seat_window_fills_the_default_screen() {
  setup_chrome
  WK_SEAT=2 "$CHROME" --user-data-dir=/tmp/p about:blank
  assert_eq "base flags and a 1280x800 window at the origin" \
    "--no-sandbox --disable-dev-shm-usage --disable-blink-features=AutomationControlled --hide-crash-restore-bubble --no-first-run --no-default-browser-check --password-store=basic --enable-unsafe-swiftshader --accept-lang=en-US,en --window-position=0,0 --window-size=1280,800 --user-data-dir=/tmp/p about:blank" \
    "$(cat "$TMP/args")"
  teardown
}

t_seat_window_reads_WK_SCREEN() {
  setup_chrome
  WK_SEAT=1 WK_SCREEN=800x600x16 "$CHROME" about:blank
  assert_contains "width and height come from WK_SCREEN" "--window-size=800,600" "$(cat "$TMP/args")"
  teardown
}

t_login_chrome_is_not_forced_to_a_seat_size() {
  setup_chrome
  "$CHROME" --start-maximized about:blank
  assert_eq "no seat: caller size is kept" \
    "--no-sandbox --disable-dev-shm-usage --disable-blink-features=AutomationControlled --hide-crash-restore-bubble --no-first-run --no-default-browser-check --password-store=basic --enable-unsafe-swiftshader --accept-lang=en-US,en --start-maximized about:blank" \
    "$(cat "$TMP/args")"
  teardown
}

t_automation_marks_are_suppressed() {
  setup_chrome
  "$CHROME" about:blank
  assert_contains "webdriver mark suppressed" \
    "--disable-blink-features=AutomationControlled" "$(cat "$TMP/args")"
  assert_contains "crash bubble hidden" \
    "--hide-crash-restore-bubble" "$(cat "$TMP/args")"
  assert_contains "software WebGL kept working" \
    "--enable-unsafe-swiftshader" "$(cat "$TMP/args")"
  teardown
}

t_exec_replaces_the_shell() {
  setup_chrome
  "$CHROME" about:blank
  # exec means the stub's pid is the pid the wrapper can see: the script does
  # not linger as a parent of the browser.
  assert_eq "stub ran" 1 "$(wc -l < "$TMP/pid" | tr -d ' ')"
  teardown
}

t_machine_id_hook_runs_before_the_browser() {
  setup_chrome
  cat > "$TMP/mid" <<'EOF'
#!/bin/sh
echo ran >> "$WK_MID_LOG"
EOF
  chmod 0755 "$TMP/mid"
  WK_MID_LOG="$TMP/mid.log" WK_MACHINE_ID_SCRIPT="$TMP/mid" "$CHROME" about:blank
  assert_eq "machine-id hook ran once" 1 "$(wc -l < "$TMP/mid.log" | tr -d ' ')"
  assert_not_contains "a missing hook never blocks launch" "mid" "$(cat "$TMP/args")"
  teardown
}

t_seat_window_fills_the_default_screen
t_seat_window_reads_WK_SCREEN
t_login_chrome_is_not_forced_to_a_seat_size
t_automation_marks_are_suppressed
t_exec_replaces_the_shell
t_machine_id_hook_runs_before_the_browser
finish

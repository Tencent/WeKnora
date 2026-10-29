#!/usr/bin/env bash
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
. "$HERE/lib_seats.sh"
DISP="$HERE/../wk-display.sh"

vnc_args() { cat "$FAKE_X/x11vnc.args.$((WK_VNC_BASE + $1))"; }
listening() { port_listening "$1" && echo 1 || echo 0; }

t_claim_is_idempotent_and_numbers_from_one() {
  setup_seats
  a="$("$DISP" claim sid-a)"; b="$("$DISP" claim sid-a)"; c="$("$DISP" claim sid-b)"
  assert_eq "first seat is 1 (:0 is the shared desktop)" 1 "$a"
  assert_eq "claim is idempotent" 1 "$b"
  assert_eq "second session gets the next seat" 2 "$c"
  teardown_seats
}

t_claim_brings_up_the_whole_stack() {
  setup_seats
  n="$("$DISP" claim sid-a)"
  assert_eq "X is up" 0 "$(xdpyinfo -display ":$n" >/dev/null 2>&1; echo $?)"
  assert_eq "xfce4-session runs" 1 "$(fake_x_pids xfce4-session | wc -l | tr -d ' ')"
  assert_eq "seat has its own session bus" 1 "$(fake_x_pids dbus-daemon | wc -l | tr -d ' ')"
  assert_eq "xfwm4 is left to the session" 0 "$(fake_x_pids xfwm4 | wc -l | tr -d ' ')"
  assert_eq "vnc listens on base+N" 1 "$(listening $((WK_VNC_BASE + n)))"
  assert_eq "token routes N to its own vnc port" "$n: 127.0.0.1:$((WK_VNC_BASE + n))" "$(cat "$WK_TOKENS_DIR/$n")"
  assert_eq "no temp token left behind" "" "$(ls -A "$WK_RUN_DIR" | grep '^\.token\.')"
  teardown_seats
}

t_x11vnc_is_loopback_and_interactive_by_default() {
  setup_seats
  n="$("$DISP" claim sid-a)"
  assert_contains "loopback only" "-localhost" "$(vnc_args "$n")"
  assert_contains "no password prompt (websockify authenticates)" "-nopw" "$(vnc_args "$n")"
  assert_not_contains "not view-only by default" "-viewonly" "$(vnc_args "$n")"
  assert_contains "default resolution" "-screen 0 1280x800x24" "$(cat "$FAKE_X/xvfb.args.$n")"
  assert_contains "never listens on tcp" "-nolisten tcp" "$(cat "$FAKE_X/xvfb.args.$n")"
  teardown_seats
}

t_viewonly_and_screen_are_opt_in_overrides() {
  setup_seats
  n="$(WK_SEAT_VIEWONLY=1 WK_SCREEN=800x600x16 "$DISP" claim sid-a)"
  assert_contains "WK_SEAT_VIEWONLY=1 adds -viewonly" "-viewonly" "$(vnc_args "$n")"
  assert_contains "WK_SCREEN overrides the resolution" "-screen 0 800x600x16" "$(cat "$FAKE_X/xvfb.args.$n")"
  teardown_seats
}

t_concurrent_claims_never_share_a_seat() {
  setup_seats
  for i in 1 2 3 4; do "$DISP" claim "s$i" > "$TMP/out.$i" & done
  wait
  assert_eq "four claims, four seats" 4 "$(cat "$TMP"/out.* | sort -u | wc -l | tr -d ' ')"
  assert_eq "four vnc listeners" 4 "$(fake_x_pids x11vnc-l | wc -l | tr -d ' ')"
  teardown_seats
}

watch_seat() { # watch_seat N -> pid of a fake VNC viewer on seat N
  # stdout closed, or the caller's $(...) waits for the viewer to exit.
  python3 -c "import socket,time; s=socket.create_connection(('127.0.0.1',$((WK_VNC_BASE + $1)))); time.sleep(300)" \
    >/dev/null 2>&1 &
  echo $!
}
wait_watched() { # wait_watched N...: until every seat shows an established viewer
  local n hex i
  for n in "$@"; do
    hex=$(printf '%04X' $((WK_VNC_BASE + n)))
    for i in $(seq 1 40); do
      awk -v hex="$hex" '$4=="01"{k=split($2,a,":"); if(toupper(a[k])==hex) f=1} END{exit !f}' /proc/net/tcp && break
      sleep 0.25
    done
  done
}

t_full_machine_fails_loudly_when_every_seat_is_watched() {
  setup_seats
  for i in 1 2 3 4; do "$DISP" claim "s$i" >/dev/null; done
  watchers=""; for n in 1 2 3 4; do watchers="$watchers $(watch_seat "$n")"; done
  wait_watched 1 2 3 4
  out="$("$DISP" claim s5 2>"$TMP/err")"; rc=$?
  assert_eq "exit 1" 1 "$rc"
  assert_eq "stdout empty" "" "$out"
  assert_contains "marker on stderr" WEKNORA_NO_FREE_SCREEN "$(cat "$TMP/err")"
  for n in 1 2 3 4; do assert_eq "watched seat $n kept" "$n" "$("$DISP" where "s$n")"; done
  kill $watchers 2>/dev/null
  teardown_seats
}

# No seat is fully idle: the unwatched seats are ranked by no app first, then
# by longest idle; a watched seat is never taken.
t_full_machine_evicts_by_rank() {
  setup_seats
  for i in 1 2 3 4; do "$DISP" claim "s$i" >/dev/null; done
  touch -d '9 minutes ago' "$WK_SEATS_DIR"/1-*   # no app, idle longest
  touch -d '5 minutes ago' "$WK_SEATS_DIR"/2-*   # no app
  touch -d '1 hour ago' "$WK_SEATS_DIR"/3-*      # app running, oldest
  touch -d '2 hours ago' "$WK_SEATS_DIR"/4-*     # watched, oldest of all
  env DISPLAY=:3 fakegui 300 & gui=$!
  viewer=$(watch_seat 4)
  wait_watched 4
  sleep 0.3
  assert_eq "app-free, longest-idle seat goes first" 1 "$("$DISP" claim s5)"
  assert_eq "evicted session lost its seat" "" "$("$DISP" where s1)"
  assert_eq "next app-free seat goes next" 2 "$("$DISP" claim s6)"
  assert_eq "an app-free seat goes before an older seat with an app" 1 "$("$DISP" claim s7)"
  assert_eq "the seat with an app was kept" 3 "$("$DISP" where s3)"
  assert_eq "the watched seat was passed over" 4 "$("$DISP" where s4)"
  kill "$gui" "$viewer" 2>/dev/null
  teardown_seats
}

t_full_machine_evicts_an_app_seat_when_nothing_else_is_left() {
  setup_seats
  for i in 1 2 3 4; do "$DISP" claim "s$i" >/dev/null; done
  touch -d '2 minutes ago' "$WK_SEATS_DIR"/1-* "$WK_SEATS_DIR"/3-* "$WK_SEATS_DIR"/4-*
  touch -d '8 minutes ago' "$WK_SEATS_DIR"/2-*
  guis=""; for n in 1 2 3 4; do env DISPLAY=:$n fakegui 300 & guis="$guis $!"; done
  sleep 0.3
  assert_eq "the longest-idle seat with an app goes" 2 "$("$DISP" claim s5)"
  assert_eq "its session lost the seat" "" "$("$DISP" where s2)"
  assert_eq "the other app seats are kept" 1 "$("$DISP" where s1)"
  kill $guis 2>/dev/null
  teardown_seats
}

t_where_allocates_nothing_on_a_miss() {
  setup_seats
  out="$("$DISP" where nobody)"; rc=$?
  assert_eq "unknown session: exit 1" 1 "$rc"
  assert_eq "unknown session: no output" "" "$out"
  assert_eq "where did not allocate" "" "$(ls -A "$WK_SEATS_DIR" 2>/dev/null | grep -v '^\.lock$')"
  assert_eq "where started nothing" "" "$(fake_x_pids)"
  teardown_seats
}

t_where_refreshes_the_seat_clock_on_a_hit() {
  setup_seats
  "$DISP" claim sid-a >/dev/null
  touch -d '1 hour ago' "$WK_SEATS_DIR"/1-*
  assert_eq "where finds the seat" 1 "$("$DISP" where sid-a)"
  age=$(( $(date +%s) - $(stat -c %Y "$WK_SEATS_DIR"/1-*) ))
  if [ "$age" -lt 5 ]; then pass "where refreshed the idleness clock (age ${age}s)"; else fail "seat clock still stale (age ${age}s)"; fi
  teardown_seats
}

t_release_kills_only_its_own_stack() {
  setup_seats
  "$DISP" claim sid-a >/dev/null; "$DISP" claim sid-b >/dev/null
  env DISPLAY=:1 fakegui 300 & g1=$!
  env DISPLAY=:2 fakegui 300 & g2=$!
  sleep 0.3
  "$DISP" release sid-a >/dev/null
  assert_eq "seat 1 vnc port closed" 0 "$(listening $((WK_VNC_BASE + 1)))"
  assert_eq "seat 1 X is gone" 1 "$(xdpyinfo -display :1 >/dev/null 2>&1; echo $?)"
  assert_eq "client on the released screen is gone" 1 "$(kill -0 "$g1" 2>/dev/null && echo 0 || echo 1)"
  assert_eq "token removed" "" "$(ls "$WK_TOKENS_DIR" | grep -x 1)"
  assert_eq "seat 2 vnc untouched" 1 "$(listening $((WK_VNC_BASE + 2)))"
  assert_eq "seat 2 X untouched" 0 "$(xdpyinfo -display :2 >/dev/null 2>&1; echo $?)"
  assert_eq "client on the other screen survives" 0 "$(kill -0 "$g2" 2>/dev/null && echo 0 || echo 1)"
  assert_eq "seat 2 still owned" 2 "$("$DISP" where sid-b)"
  assert_eq "released session has no seat" "" "$("$DISP" where sid-a)"
  assert_eq "release of an unknown session is harmless" 0 "$("$DISP" release nobody >/dev/null; echo $?)"
  teardown_seats
}

t_reap_only_takes_idle_unwatched_seats() {
  setup_seats
  for i in 1 2 3 4; do "$DISP" claim "s$i" >/dev/null; done
  for n in 1 2 3 4; do touch -d '1 hour ago' "$WK_SEATS_DIR"/$n-*; done
  env DISPLAY=:2 fakegui 300 & busy=$!   # seat 2: has a GUI client
  python3 -c "import socket,time; s=socket.create_connection(('127.0.0.1',$((WK_VNC_BASE + 3)))); time.sleep(30)" & viewer=$!   # seat 3: has a viewer
  sleep 0.5
  out="$("$DISP" reap)"
  assert_eq "only seat 1 and 4 are reclaimed" 2 "$out"
  assert_eq "busy seat kept" 2 "$("$DISP" where s2)"
  assert_eq "watched seat kept" 3 "$("$DISP" where s3)"
  assert_eq "reclaimed seat has no table entry" "" "$("$DISP" where s1)"
  assert_eq "reclaimed seat lost its token" "" "$(ls "$WK_TOKENS_DIR" | grep -x -e 1 -e 4)"
  assert_eq "reclaimed seat vnc closed" 0 "$(listening $((WK_VNC_BASE + 4)))"
  assert_eq "kept seat vnc still up" 1 "$(listening $((WK_VNC_BASE + 3)))"
  kill "$busy" "$viewer" 2>/dev/null
  teardown_seats
}

t_young_seats_are_never_reaped() {
  setup_seats
  "$DISP" claim s1 >/dev/null
  assert_eq "fresh seat kept" 0 "$("$DISP" reap)"
  assert_eq "still owned" 1 "$("$DISP" where s1)"
  teardown_seats
}

t_idle_seat_with_only_a_shell_on_it_is_reaped() {
  setup_seats
  "$DISP" claim s1 >/dev/null
  touch -d '1 hour ago' "$WK_SEATS_DIR"/1-*
  env DISPLAY=:1 bash -c 'sleep 30' & sh_pid=$!   # a shell (and its sleep) is not a screen client
  sleep 0.3
  assert_eq "shell-only seat is reclaimed" 1 "$("$DISP" reap)"
  kill "$sh_pid" 2>/dev/null
  teardown_seats
}

t_full_machine_reaps_an_idle_seat_to_make_room() {
  setup_seats
  for i in 1 2 3 4; do "$DISP" claim "s$i" >/dev/null; done
  touch -d '1 hour ago' "$WK_SEATS_DIR"/3-*
  assert_eq "new session takes the reclaimed seat" 3 "$("$DISP" claim s5)"
  assert_eq "its stack is up" 1 "$(listening $((WK_VNC_BASE + 3)))"
  assert_eq "the evicted session lost its seat" "" "$("$DISP" where s3)"
  teardown_seats
}

t_claim_repairs_a_half_dead_stack() {
  setup_seats
  n="$("$DISP" claim sid-a)"
  for p in $(fake_x_pids x11vnc) $(fake_x_pids x11vnc-l); do kill -KILL "$p" 2>/dev/null; done
  sleep 0.5
  assert_eq "vnc is down" 0 "$(listening $((WK_VNC_BASE + n)))"
  assert_eq "X survived" 0 "$(xdpyinfo -display ":$n" >/dev/null 2>&1; echo $?)"
  assert_eq "same seat on re-claim" "$n" "$("$DISP" claim sid-a)"
  assert_eq "vnc restored by the next claim" 1 "$(listening $((WK_VNC_BASE + n)))"
  assert_eq "no second Xvfb" 1 "$(fake_x_pids Xvfb | wc -l | tr -d ' ')"
  assert_eq "no second session" 1 "$(fake_x_pids xfce4-session | wc -l | tr -d ' ')"
  teardown_seats
}

t_claim_clears_a_stale_x_lock() {
  setup_seats
  echo 999999 > "$WK_X_LOCK_DIR/.X1-lock"   # left behind by a crash; the fake Xvfb refuses to start over it
  n="$("$DISP" claim sid-a)"
  assert_eq "claim still succeeds" 1 "$n"
  assert_eq "X is up" 0 "$(xdpyinfo -display :1 >/dev/null 2>&1; echo $?)"
  teardown_seats
}

t_rejects_hostile_ids() {
  setup_seats
  "$DISP" claim 'a b' 2>/dev/null; assert_eq "space rejected" 2 $?
  "$DISP" claim '../x' 2>/dev/null; assert_eq "path rejected" 2 $?
  "$DISP" claim '' 2>/dev/null; assert_eq "empty rejected" 2 $?
  "$DISP" claim 'a;b' 2>/dev/null; assert_eq "shell metachar rejected" 2 $?
  "$DISP" claim "$(printf 'a%.0s' $(seq 65))" 2>/dev/null; assert_eq "65 chars rejected" 2 $?
  "$DISP" where '../x' 2>/dev/null; assert_eq "where rejects too" 2 $?
  "$DISP" release 'a b' 2>/dev/null; assert_eq "release rejects too" 2 $?
  assert_eq "nothing was allocated" "" "$(ls -A "$WK_SEATS_DIR" | grep -v '^\.lock$')"
  teardown_seats
}

t_claim_releases_the_seat_lock() {
  # A daemon that inherits the seat lock fd keeps the flock after the claim
  # returns (dbus-daemon --system did exactly this), and every later claim of
  # that seat queues until its Exec times out. The lock must be free once
  # claim exits, and the system-dbus spawn must close the fd.
  setup_seats
  n="$("$DISP" claim sid-a)"
  flock -n "$WK_RUN_DIR/seat-$n.lock" true; rc=$?
  assert_eq "seat lock free after claim" 0 "$rc"
  n2="$("$DISP" claim sid-a)"
  assert_eq "second claim still returns instantly" "$n" "$n2"
  assert_contains "system dbus must not hold the seat lock" \
    "7>&- 8>&- 9>&-" "$(grep -F 'dbus-daemon --system --fork' "$DISP")"
  teardown_seats
}

t_x_probes_are_bounded_under_the_seat_lock() {
  # Every X probe runs while seat-N.lock is held; a wedged X server (accepts
  # the connection, never replies) would wedge the seat forever without a
  # per-probe timeout — the same lock-hang class as the dbus fd leak.
  assert_contains "xdpyinfo probes are bounded" "timeout 2 xdpyinfo" \
    "$(grep -F 'xdpyinfo -display' "$DISP")"
  assert_contains "xset is bounded" "timeout 5 xset s off -dpms" \
    "$(grep -F 'xset s off' "$DISP")"
}

t_daemons_run_from_a_neutral_cwd() {
  # websockify inherited the starting Exec's cwd (a session workspace); when
  # that session was deleted, every desktop dial on the machine died with
  # ENOENT before any HTTP response. Scripts must cd to a neutral cwd before
  # spawning daemons.
  assert_eq "wk-display drops the client cwd" 1 \
    "$(grep -c '^cd /$' "$DISP")"
}

t_claim_is_idempotent_and_numbers_from_one
t_claim_brings_up_the_whole_stack
t_x11vnc_is_loopback_and_interactive_by_default
t_viewonly_and_screen_are_opt_in_overrides
t_concurrent_claims_never_share_a_seat
t_full_machine_fails_loudly_when_every_seat_is_watched
t_full_machine_evicts_by_rank
t_full_machine_evicts_an_app_seat_when_nothing_else_is_left
t_where_allocates_nothing_on_a_miss
t_where_refreshes_the_seat_clock_on_a_hit
t_release_kills_only_its_own_stack
t_reap_only_takes_idle_unwatched_seats
t_young_seats_are_never_reaped
t_idle_seat_with_only_a_shell_on_it_is_reaped
t_full_machine_reaps_an_idle_seat_to_make_room
t_claim_repairs_a_half_dead_stack
t_claim_clears_a_stale_x_lock
t_rejects_hostile_ids
t_claim_releases_the_seat_lock
t_x_probes_are_bounded_under_the_seat_lock
t_daemons_run_from_a_neutral_cwd

t_sid_of_maps_seat_back_to_session() {
  setup_seats
  "$DISP" claim sid-with-hyphens >/dev/null
  n=$("$DISP" where sid-with-hyphens)
  assert_eq "sid-of returns the holder" "sid-with-hyphens" "$("$DISP" sid-of "$n")"
  "$DISP" release sid-with-hyphens >/dev/null
  "$DISP" sid-of "$n" >/dev/null 2>&1
  assert_eq "sid-of fails on a free seat" 1 "$?"
  assert_eq "empty output on failure" "" "$("$DISP" sid-of "$n" 2>/dev/null)"
  "$DISP" sid-of not-a-number >/dev/null 2>&1
  assert_eq "bad seat rejected" 2 "$?"
  teardown_seats
}
t_sid_of_maps_seat_back_to_session
finish

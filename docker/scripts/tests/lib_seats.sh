# Environment for the wk-display tests (sourced after lib.sh).
# Fake Xvfb/xfwm4/x11vnc/xdpyinfo live in stubs/seatstub.sh; every process they start inherits
# FAKE_X (unique per test), which is how teardown finds and kills all of them.

port_listening() {
  local hex; hex=$(printf '%04X' "$1")
  cat /proc/net/tcp | awk -v hex="$hex" '$4=="0A"{n=split($2,a,":"); if(toupper(a[n])==hex) f=1} END{exit !f}'
}

# Space-separated own pid and ancestors; sweeps must never kill the test shell itself.
_self_chain() {
  local p=${BASHPID:-$$} out=""
  while [ -n "$p" ] && [ "$p" -gt 1 ] 2>/dev/null; do
    out="$out $p"
    p=$(sed 's/^.*) //' "/proc/$p/stat" 2>/dev/null | awk '{print $2}')
  done
  echo "$out "
}

# fake_x_pids [comm]: pids of this test's processes (optionally filtered by comm), self chain excluded.
fake_x_pids() {
  local p skip; skip=$(_self_chain)
  for p in /proc/[0-9]*; do
    p=${p#/proc/}
    case "$skip" in *" $p "*) continue ;; esac
    grep -aqzx "FAKE_X=$FAKE_X" "/proc/$p/environ" 2>/dev/null || continue
    [ -z "${1:-}" ] || [ "$(cat "/proc/$p/comm" 2>/dev/null)" = "$1" ] && echo "$p"
  done
}

_free_port_base() {
  local b i ok
  while :; do
    b=$((26000 + RANDOM % 3000)); ok=1
    for i in 0 1 2 3 4 5; do port_listening $((b + i)) && ok=0; done
    [ "$ok" = 1 ] && { echo "$b"; return; }
  done
}

setup_seats() {
  TMP="$(mktemp -d)"; export TMP
  export FAKE_X="$TMP/x"
  mkdir -p "$FAKE_X" "$TMP/bin" "$TMP/run" "$TMP/seats" "$TMP/xlock" "$TMP/xsock"
  local n
  for n in Xvfb xfwm4 x11vnc xdpyinfo xfce4-session dbus-daemon; do
    ln -s "$TESTS_DIR/stubs/seatstub.sh" "$TMP/bin/$n"
  done
  ln -s "$(command -v python3)" "$TMP/bin/x11vnc-l"   # listener child of the fake x11vnc, comm=x11vnc-l
  ln -s /bin/sleep "$TMP/bin/fakegui"                  # comm=fakegui: stands for a GUI client on a screen
  export PATH="$TMP/bin:$ORIG_PATH"
  export WK_SEATS_DIR="$TMP/seats" WK_RUN_DIR="$TMP/run" WK_TOKENS_DIR="$TMP/run/tokens.d"
  export WK_X_LOCK_DIR="$TMP/xlock" WK_X_SOCK_DIR="$TMP/xsock"
  export WK_VNC_BASE; WK_VNC_BASE=$(_free_port_base)
  export WK_SEAT_MAX=4 WK_SEAT_IDLE=600
  unset WK_SCREEN WK_SEAT_VIEWONLY
  trap teardown_seats EXIT
}

teardown_seats() {
  [ -n "${FAKE_X:-}" ] || return 0
  local p i
  for i in 1 2 3; do
    for p in $(fake_x_pids); do kill -KILL "$p" 2>/dev/null; done
    [ -z "$(fake_x_pids)" ] && break
    sleep 0.2
  done
  export PATH="$ORIG_PATH"
  rm -rf "$TMP"
  FAKE_X=""
}

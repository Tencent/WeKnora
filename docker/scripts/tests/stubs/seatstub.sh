#!/usr/bin/env bash
# Fake Xvfb / xfwm4 / x11vnc / xdpyinfo for wk-display tests.
# The process comm is the symlink name (we never exec), so comm matches the real binary name.
# Every process started from here inherits FAKE_X, which the test teardown uses to sweep them.
set -u
# A shebang re-execs bash, so /proc/self/comm would be "bash" and the seat
# scanner (which matches Xvfb/xfwm4/x11vnc) would not see this process.
# Writing comm is allowed for the current task and keeps the symlink name.
comm=$(basename "$0")
printf '%s' "${comm:0:15}" > /proc/self/comm 2>/dev/null || true
X="${FAKE_X:?}"
LOCKDIR="${WK_X_LOCK_DIR:?}"

hold() { # hold <cleanup-cmd>: block until TERM, then clean up
  sleep 3600 &
  local c=$!
  trap "kill $c 2>/dev/null; $1; exit 0" TERM
  wait $c
}

case "$(basename "$0")" in
  xdpyinfo) # xdpyinfo -display :N
    d="${2#:}"
    [ -f "$X/up.$d" ] && kill -0 "$(cat "$X/up.$d")" 2>/dev/null
    ;;
  Xvfb) # Xvfb :N ...
    d="${1#:}"
    # Like the real server: refuse to start while a lock file exists.
    [ -e "$LOCKDIR/.X$d-lock" ] && { echo "Server is already active for display $d" >&2; exit 1; }
    echo "$*" > "$X/xvfb.args.$d"
    echo $$ > "$LOCKDIR/.X$d-lock"
    echo $$ > "$X/up.$d"
    hold "rm -f $X/up.$d $LOCKDIR/.X$d-lock"
    ;;
  xfwm4|xfce4-session|dbus-daemon)
    hold :
    ;;
  x11vnc) # x11vnc ... -rfbport P ...
    port=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "-rfbport" ] && port="$a"
      prev="$a"
    done
    echo "$*" > "$X/x11vnc.args.$port"
    # The listener gets its own comm (x11vnc-l) so tests can tell it apart.
    "$(dirname "$0")/x11vnc-l" -c "import socket,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(('127.0.0.1',$port)); s.listen(5); time.sleep(3600)" &
    child=$!
    trap "kill $child 2>/dev/null; exit 0" TERM
    wait $child
    ;;
esac

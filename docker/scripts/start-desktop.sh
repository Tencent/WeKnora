#!/bin/sh
# Idempotent desktop stack for WeKnora sandbox images.
# Safe to re-run after pause/resume; serialised with flock so concurrent Exec
# (dual tabs, retries, multiple replicas) cannot split the VNC secret.
set -eu
export DISPLAY=:0
# UTF-8 for every daemon this script starts (XFCE session, xfce4-terminal,
# mousepad). Without it they inherit POSIX/C from python:slim and UTF-8
# files render as mojibake; the WeKnora terminal tab is unaffected because
# it is drawn in the browser. Prefer an image-provided LANG, fall back here
# so a wrapped older image still starts a UTF-8 session.
export LANG="${LANG:-C.UTF-8}"
export LC_ALL="${LC_ALL:-C.UTF-8}"
mkdir -p /run/desktop

# Readiness is a LISTEN check read out of /proc, never a connection. Dialling
# 5900 spends an RFB client slot and dialling 6080 parks websockify in
# readline(); a probe must not perturb the stack it measures. (This also
# sidesteps the old nc/dash problems: nc is not in the image and dash has no
# /dev/tcp, and under `set -eu` a 127 exit from `if nc -z ...` does not trip
# set -e, so the script would spin to its timeout with the desktop already up.)
port_open() {
    hex=$(printf '%04X' "$1")
    cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk -v hex="$hex" '
        $4 == "0A" {
            n = split($2, addr, ":")
            if (toupper(addr[n]) == hex) { found = 1 }
        }
        END { exit !found }
    '
}

# pgrep matches zombies. x11vnc -bg leaves one behind whenever PID 1 does not
# reap it (envd does not), and a pgrep-guarded step 7 then skips the restart
# for the life of the sandbox: websockify keeps answering on 6080, DialDesktop
# gets its 101, and the RFB hello never comes. Steps guarded by a listening
# port are immune; this is for the ones with no port of their own.
proc_alive() {
    for pid in $(pgrep -x "$1" 2>/dev/null); do
        state=$(awk '/^State:/ { print $2; exit }' "/proc/$pid/status" 2>/dev/null || true)
        [ "$state" = "Z" ] || return 0
    done
    return 1
}

# 1. Already ready: both listeners, not just websockify. 6080-only lets
#    DialDesktop 101 while x11vnc is dead; the RFB hello never comes.
if port_open 6080 && port_open 5900; then exit 0; fi

# 2. Serialise the whole start section. Concurrent triggers are normal
#    (dual tabs, timeout retries, multi-replica). Every step below is
#    check-then-act: without the lock two instances both see "secret missing"
#    and each write their own, so websockify uses A while the backend reads B
#    — intermittent 401. A Redis connection lock cannot serialise this Exec.
#
#    Every daemon started below MUST close fd 9 (`9>&-`). A child that
#    inherits it keeps holding the lock after this script exits, and the next
#    run that misses the fast path blocks on flock forever — the Exec times
#    out at 60s (exit=-1) and 6080 never comes back.
exec 9>/run/desktop/.lock
flock 9

# Recheck after waiting for the lock: a previous holder may have finished.
if port_open 6080 && port_open 5900; then exit 0; fi

# 3. Per-sandbox random secret (first start only). Used by websockify's
#    BasicHTTPAuth handshake; never sent to the browser. Write .tmp then
#    rename so a reader that sees the file also sees complete contents.
if [ ! -f /run/desktop/secret ]; then
  umask 077
  tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32 > /run/desktop/secret.tmp
  mv /run/desktop/secret.tmp /run/desktop/secret
fi

# dbus-uuidgen --ensure: XFCE/dbus fail on a missing /etc/machine-id, which
# slim images often omit. Harmless when the file already exists.
if command -v dbus-uuidgen >/dev/null 2>&1; then
  dbus-uuidgen --ensure >/dev/null 2>&1 || true
fi

# Every daemon below is started through this. Two things must happen or the
# desktop "fails" while actually running:
#   - close fd 9, or the daemon keeps holding the flock after this script
#     exits and the next run that misses the fast path blocks forever;
#   - give it stdio of its own, or it holds the caller's stdout/stderr and
#     envd's Exec never returns (it waits for those to close), which surfaces
#     as exit=-1 at the 60s timeout.
spawn() {
    name=$1
    shift
    "$@" </dev/null >>"/run/desktop/$name.log" 2>&1 9>&- &
}

# 4. Xvfb
proc_alive Xvfb || \
  spawn xvfb Xvfb :0 -ac -screen 0 1280x800x24 -nolisten tcp

# 4b. Wait until X actually accepts connections. Probe state, do not sleep a
#     fixed delay: startxfce4 before X is listening exits with "cannot open
#     display", later steps still run, and the failure looks like "6080 is up
#     but the screen is black / no window manager".
i=0
while [ $i -lt 40 ]; do
  xdpyinfo -display :0 >/dev/null 2>&1 && break
  i=$((i + 1))
  sleep 0.25
done
xdpyinfo -display :0 >/dev/null 2>&1 || exit 1

# 5. dbus + XFCE. Use xfce4-session, not startxfce4: the latter tries to
#    launch its own X server and fails with "X server already running" once
#    Xvfb is up. The session still starts, but the warning is noisy and the
#    extra compositor fight is real (xfwm4: another compositing manager).
proc_alive xfce4-session || {
  mkdir -p /run/dbus
  dbus-daemon --system --fork </dev/null >>/run/desktop/dbus.log 2>&1 9>&- || true
  spawn xfce4-session xfce4-session
}

# 6. Disable screensaver / DPMS — clock jumps after resume lock or blank the screen.
xset s off -dpms 2>/dev/null || true
xfconf-query -c xfce4-session -p /startup/screensaver/enabled -n -t bool -s false 2>/dev/null || true

# 7. x11vnc: localhost only; RFB security type None (auth is on websockify).
#    Do not pass -ping: it pushes a 1x1 framebuffer update every second and
#    would defeat any byte-counting idle detector.
#
#    Guard on the port, not on pgrep: a zombie x11vnc is what silently turns
#    this step into a no-op. A duplicate only ever loses the bind and exits.
port_open 5900 || \
  x11vnc -display :0 -localhost -rfbport 5900 -nopw \
         -forever -shared -wait 50 -noxdamage -repeat -bg \
         </dev/null >>/run/desktop/x11vnc.log 2>&1 9>&-

# 8. websockify: the only listener facing the world. Handshake requires
#    Authorization: Basic. This is the only gate on Cube's host-port NAT
#    path that bypasses CubeProxy. Port-guarded for the same reason as 7.
port_open 6080 || \
  spawn websockify websockify --auth-plugin BasicHTTPAuth \
             --auth-source "weknora:$(cat /run/desktop/secret)" \
             --heartbeat 30 6080 127.0.0.1:5900

# 9. Wait for ready (lock held so waiters queue instead of starting twice).
i=0
while [ $i -lt 60 ]; do
  port_open 6080 && port_open 5900 && exit 0
  i=$((i + 1))
  sleep 0.5
done
exit 1

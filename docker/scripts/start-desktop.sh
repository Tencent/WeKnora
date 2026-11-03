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

# Neutral cwd before any daemon spawns. This script runs from an Exec whose
# cwd is a session's workspace (/workspace/sessions/<sid>); every daemon
# spawned here inherits it, and deleting that session leaves websockify's
# cwd pointing at a deleted directory — every later connection then dies
# with "handler exception: [Errno 2] No such file or directory" and no HTTP
# response at all (a bad handshake on the backend, for every session on the
# machine, since websockify is shared). Same inheritance class as the fd 9
# and stdio rules below.
cd /

# --proxy-only starts websockify and nothing on :0. Per-session screens are
# Xvfb :N, brought up by wk-display; this process only routes tokens to them.
proxy_only=0
if [ "${1:-}" = "--proxy-only" ]; then
  proxy_only=1
fi

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

# Full mode needs both listeners. 6080-only lets DialDesktop 101 while
# x11vnc on :0 is dead. proxy-only has no :0; 6080 is the whole stack.
desktop_ready() {
  if [ "$proxy_only" -eq 1 ]; then
    port_open 6080
    return
  fi
  port_open 6080 && port_open 5900
}

# 1. Already ready.
if desktop_ready; then exit 0; fi

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
if desktop_ready; then exit 0; fi

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

# 4-8b. The shared :0 desktop. Skipped in --proxy-only: that mode must not
# start Xvfb, XFCE, x11vnc on 5900, or anything else on :0.
if [ "$proxy_only" -eq 0 ]; then

# 4. Xvfb
proc_alive Xvfb || \
  spawn xvfb Xvfb :0 -ac -screen 0 1280x800x24 -nolisten tcp

# 4b. Wait until X actually accepts connections. Probe state, do not sleep a
#     fixed delay: startxfce4 before X is listening exits with "cannot open
#     display", later steps still run, and the failure looks like "6080 is up
#     but the screen is black / no window manager". Every probe carries its
#     own timeout: fd 9 (the start lock) is held here, and a wedged X server
#     (accepts, never replies) would block an unbounded probe forever.
i=0
while [ $i -lt 40 ]; do
  timeout 2 xdpyinfo -display :0 >/dev/null 2>&1 && break
  i=$((i + 1))
  sleep 0.25
done
timeout 2 xdpyinfo -display :0 >/dev/null 2>&1 || exit 1

# 5. dbus + XFCE. Use xfce4-session, not startxfce4: the latter tries to
#    launch its own X server and fails with "X server already running" once
#    Xvfb is up. The session still starts, but the warning is noisy and the
#    extra compositor fight is real (xfwm4: another compositing manager).
proc_alive xfce4-session || {
  mkdir -p /run/dbus
  dbus-daemon --system --fork </dev/null >>/run/desktop/dbus.log 2>&1 9>&- || true
  spawn xfce4-session xfce4-session
}

# 6. Disable screensaver / DPMS — clock jumps after resume lock or blank the
#    screen. timeout: fd 9 is held; a wedged X server wedges the lock too.
timeout 5 xset s off -dpms 2>/dev/null || true
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

# Token 0 is the shared :0 screen. Seat tokens are written by wk-display.
mkdir -p /run/desktop/tokens.d
printf '0: 127.0.0.1:5900\n' > /run/desktop/tokens.d/0.tmp
mv /run/desktop/tokens.d/0.tmp /run/desktop/tokens.d/0

fi

# 8. websockify: the only listener facing the world. Handshake requires
#    Authorization: Basic. TokenFile picks the screen (?token=N -> 127.0.0.1:590N).
#    There is no fixed target: a dial without a token is rejected, which is
#    why this image must not run in front of a backend that omits the token
#    query. Publish the backend first.
mkdir -p /run/desktop/tokens.d
port_open 6080 || \
  spawn websockify websockify --auth-plugin BasicHTTPAuth \
             --auth-source "weknora:$(cat /run/desktop/secret)" \
             --token-plugin TokenFile \
             --token-source /run/desktop/tokens.d \
             --heartbeat 30 6080

# 8b. Browser sync: shares login cookies and localStorage between the session
#     browsers and saves them for the next launch. A second copy exits via flock.
[ -x /usr/local/bin/wk-browser-sync ] && \
  spawn browsersync /usr/local/bin/wk-browser-sync

# 8c. Fingerprint governor: one UA / hardware / time zone identity for every
#     session browser. A second copy exits via flock.
[ -x /usr/local/bin/wk-ua-governor ] && \
  spawn uagovernor /usr/local/bin/wk-ua-governor

# 9. Wait for ready (lock held so waiters queue instead of starting twice).
i=0
while [ $i -lt 60 ]; do
  if desktop_ready; then exit 0; fi
  i=$((i + 1))
  sleep 0.5
done
exit 1

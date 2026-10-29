#!/usr/bin/env bash
# wk-display: one virtual screen ("seat") per WeKnora session. Seat N <-> session id, screen :N.
# Seats are numbered 1..SEAT_MAX. Display :0 (the shared legacy desktop) is not in the seat table.
# No "set -e": exit codes are captured explicitly.
#
#   claim <sid>    take a seat (idempotent), make sure its screen stack runs, print N.
#                  Full machine: reclaim fully idle seats; failing that, evict one seat
#                  (no viewer, then no app, then longest idle). Only when every seat has
#                  a viewer: exit 1 + stderr WEKNORA_NO_FREE_SCREEN, stdout empty.
#   where <sid>    print the held seat N, refreshing that seat's idleness clock;
#                  exit 1 (no output, no allocation) when none is held.
#   sid-of <N>     print the session id holding seat N; exit 1 when free.
#   release <sid>  drop the seat record and kill every process on that screen.
#   reap           reclaim idle seats, print how many were reclaimed.
#   up <N>         internal: idempotently start/repair the screen stack of seat N.
#   list           diagnostics.
#
# Every path/parameter can be overridden through WK_* environment variables (used by the tests).
set -u

# Neutral cwd before any spawn. This script runs from an Exec whose cwd is a
# session's workspace; deleting that session would leave every daemon started
# here with a cwd pointing at a deleted directory (the same inheritance class
# as fd 7/8/9 below — websockify died exactly this way in start-desktop.sh).
cd /

SEATS_DIR="${WK_SEATS_DIR:-/var/lib/weknora/browser/.seats}"
RUN_DIR="${WK_RUN_DIR:-/run/desktop}"
TOKENS_DIR="${WK_TOKENS_DIR:-$RUN_DIR/tokens.d}"
X_LOCK_DIR="${WK_X_LOCK_DIR:-/tmp}"
X_SOCK_DIR="${WK_X_SOCK_DIR:-/tmp/.X11-unix}"
SEAT_MAX="${WK_SEAT_MAX:-8}"
SEAT_IDLE="${WK_SEAT_IDLE:-600}"
SCREEN="${WK_SCREEN:-1280x800x24}"
VNC_BASE="${WK_VNC_BASE:-5900}"
VIEWONLY="${WK_SEAT_VIEWONLY:-0}"
WM="${WK_SEAT_WM:-xfwm4}"   # xfwm4 | none

case "$SEAT_MAX" in ""|*[!0-9]*) SEAT_MAX=8 ;; esac
[ "$SEAT_MAX" -gt 99 ] && SEAT_MAX=99
[ "$SEAT_MAX" -lt 1 ] && SEAT_MAX=1
case "$SEAT_IDLE" in ""|*[!0-9]*) SEAT_IDLE=600 ;; esac

# Read /proc/net/tcp, never dial (same discipline as start-desktop.sh).
tcp_count() { # tcp_count <port> <state-hex> -> number of sockets whose LOCAL port matches
  local hex; hex=$(printf '%04X' "$1")
  cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk -v hex="$hex" -v st="$2" '
    $4 == st { n = split($2, a, ":"); if (toupper(a[n]) == hex) c++ }
    END { print c + 0 }'
}
port_open() { [ "$(tcp_count "$1" 0A)" -gt 0 ]; }
viewers()   { tcp_count "$1" 01; }

valid_sid()  { case "$1" in ""|*[!A-Za-z0-9._-]*) return 1 ;; esac; [ "${#1}" -le 64 ]; }
valid_seat() { case "$1" in ""|*[!0-9]*) return 1 ;; esac; [ "$1" -ge 1 ] && [ "$1" -le "$SEAT_MAX" ]; }

# Own pid and all ancestors. Scans must skip them: the calling shell may have DISPLAY=:N exported
# and would otherwise be mistaken for a client on that screen.
self_chain() {
  local p=${BASHPID:-$$} out=""
  while [ -n "$p" ] && [ "$p" -gt 1 ] 2>/dev/null; do
    out="$out $p"
    p=$(sed 's/^.*) //' "/proc/$p/stat" 2>/dev/null | awk '{print $2}')
  done
  echo "$out "
}

seat_pids() { # processes whose environment carries WK_SEAT=N or DISPLAY=:N[.S] (self chain excluded)
  local n=$1 p skip; skip=$(self_chain)
  for p in /proc/[0-9]*; do
    p=${p#/proc/}
    case "$skip" in *" $p "*) continue ;; esac
    [ -r "/proc/$p/environ" ] || continue
    grep -aqzE "^(WK_SEAT=$n|DISPLAY=:$n(\.[0-9]+)?)$" "/proc/$p/environ" 2>/dev/null && echo "$p"
  done
}
comm_of() { cat "/proc/$1/comm" 2>/dev/null; }
# The fake/real listener child of x11vnc may carry its own comm (x11vnc-*): it is part of the stack.
# Applications the user or agent opened (xfce4-terminal, mousepad, chrome)
# count as clients and must stay out of this
# list, or a full table will reap a seat that still has their window.
is_stack() {
  case "$1" in
    Xvfb|xfwm4|x11vnc|x11vnc-*|dbus-daemon|xfdesktop|xfsettingsd|xfconfd|tumblerd) return 0 ;;
    xfce4-session|xfce4-panel|xfce4-notifyd|xfce4-power-m|xfce4-power-man|xfce4-screensav) return 0 ;;
    gvfsd|gvfsd-*|gvfs-*|at-spi*|polkit-*) return 0 ;;
  esac
  return 1
}
is_ignorable() { case "$1" in bash|sh|dash|zsh|sleep) return 0 ;; esac; return 1; }
seat_comm_pids() { # seat_comm_pids N comm
  local p; for p in $(seat_pids "$1"); do [ "$(comm_of "$p")" = "$2" ] && echo "$p"; done
}
seat_busy() { # any process on the screen that is neither screen stack nor a plain shell
  local p c
  for p in $(seat_pids "$1"); do
    c=$(comm_of "$p"); [ -n "$c" ] || continue
    is_stack "$c" && continue
    is_ignorable "$c" && continue
    return 0
  done
  return 1
}

kill_seat() { # clients first, then the stack; KILL as a last resort
  local n=$1 p c i
  for p in $(seat_pids "$n"); do c=$(comm_of "$p"); is_stack "$c" || kill -TERM "$p" 2>/dev/null; done
  sleep 0.3
  for p in $(seat_pids "$n"); do kill -TERM "$p" 2>/dev/null; done
  for i in 1 2 3 4 5 6 7 8; do [ -z "$(seat_pids "$n")" ] && break; sleep 0.25; done
  for p in $(seat_pids "$n"); do kill -KILL "$p" 2>/dev/null; done
  rm -f "$TOKENS_DIR/$n" "$X_LOCK_DIR/.X$n-lock" "$X_SOCK_DIR/X$n"
}

# ---- screen stack ----
seat_bus() { echo "unix:path=$RUN_DIR/seat-$1/bus"; }

# Each seat gets its own config, cache and session bus. Sharing /root/.config
# makes the second xfce4-session attach to the first one's xfconf and the
# panel never appears on the new screen.
prepare_seat_home() {
  local n=$1 d="$RUN_DIR/seat-$n"
  mkdir -p "$d/xdg" "$d/config" "$d/cache" "$d/data"
  chmod 0700 "$d/xdg" "$d/config" "$d/cache" "$d/data"
  [ -e "$d/config/.seeded" ] && return 0
  if [ -d /etc/xdg/xfce4 ]; then
    mkdir -p "$d/config/xfce4"
    cp -a /etc/xdg/xfce4/. "$d/config/xfce4/" 2>/dev/null || true
  fi
  if [ -d /root/.config/xfce4 ]; then
    mkdir -p "$d/config/xfce4"
    cp -a /root/.config/xfce4/. "$d/config/xfce4/" 2>/dev/null || true
  fi
  : > "$d/config/.seeded"
}

spawn() { # spawn N name cmd...  closes fd 7/8/9 (lock inheritance), owns stdio, tags WK_SEAT/DISPLAY
  local n=$1 name=$2; shift 2
  prepare_seat_home "$n"
  env WK_SEAT="$n" DISPLAY=":$n" \
    XDG_RUNTIME_DIR="$RUN_DIR/seat-$n/xdg" \
    XDG_CONFIG_HOME="$RUN_DIR/seat-$n/config" \
    XDG_CACHE_HOME="$RUN_DIR/seat-$n/cache" \
    XDG_DATA_HOME="$RUN_DIR/seat-$n/data" \
    DBUS_SESSION_BUS_ADDRESS="$(seat_bus "$n")" \
    setsid "$@" \
    </dev/null >>"$RUN_DIR/seat-$n/$name.log" 2>&1 7>&- 8>&- 9>&- &
}

seat_has_comm() { [ -n "$(seat_comm_pids "$1" "$2")" ]; }

ensure_bus() { # private session bus; a shared bus merges every seat's panel
  local n=$1
  seat_has_comm "$n" dbus-daemon && return 0
  command -v dbus-daemon >/dev/null 2>&1 || return 1
  rm -f "$RUN_DIR/seat-$n/bus"
  spawn "$n" dbus dbus-daemon --session --nofork --address="$(seat_bus "$n")"
  wait_for 40 seat_has_comm "$n" dbus-daemon
}

# xfce4-session starts the panel, desktop icons, applications menu and xfwm4.
# Starting xfwm4 ourselves as well makes the two compositors fight.
ensure_desktop() {
  local n=$1
  if command -v xfce4-session >/dev/null 2>&1 && ensure_bus "$n"; then
    seat_has_comm "$n" xfce4-session || spawn "$n" xfce4-session xfce4-session
    return 0
  fi
  if [ "$WM" = xfwm4 ] && ! seat_has_comm "$n" xfwm4; then
    spawn "$n" xfwm4 xfwm4 --compositor=off
  fi
}

quiet_session() {
  local n=$1
  # timeout: this runs while seat-N.lock is held, and a wedged X server
  # (accepts the connection, never replies) would wedge the seat forever —
  # the same lock-hang class as the dbus-daemon fd leak.
  DISPLAY=":$n" timeout 5 xset s off -dpms >/dev/null 2>&1 || true
  command -v xfconf-query >/dev/null 2>&1 || return 0
  # xfconfd comes up with the session. One short try; a missing bus must not
  # stall the claim.
  timeout 1 env DISPLAY=":$n" DBUS_SESSION_BUS_ADDRESS="$(seat_bus "$n")" \
    XDG_RUNTIME_DIR="$RUN_DIR/seat-$n/xdg" XDG_CONFIG_HOME="$RUN_DIR/seat-$n/config" \
    xfconf-query -c xfce4-session -p /startup/screensaver/enabled -n -t bool -s false \
    >/dev/null 2>&1 || true
}
wait_for() { # wait_for <tries> cmd...  poll a condition instead of sleeping blindly
  local tries=$1 i; shift
  for ((i = 0; i < tries; i++)); do "$@" >/dev/null 2>&1 && return 0; sleep 0.25; done
  return 1
}

up_locked() {
  local n=$1 port=$((VNC_BASE + n)) vnc_args
  # Every X probe carries its own timeout: a wedged X server accepts the
  # connection and then never replies, and an unbounded probe here would
  # hold seat-N.lock forever — every later claim of the seat queues until
  # its Exec times out (the same wedge class as the dbus-daemon fd leak).
  if ! timeout 2 xdpyinfo -display ":$n" >/dev/null 2>&1; then
    # A stale lock/socket left by a crash or pause/resume makes Xvfb refuse to start.
    if [ -z "$(seat_comm_pids "$n" Xvfb)" ]; then
      rm -f "$X_LOCK_DIR/.X$n-lock" "$X_SOCK_DIR/X$n"
    fi
    spawn "$n" xvfb Xvfb ":$n" -ac -screen 0 "$SCREEN" -nolisten tcp
    wait_for 40 timeout 2 xdpyinfo -display ":$n" || { echo "wk-display: Xvfb :$n did not come up" >&2; return 1; }
  fi
  # The shared :0 path starts the system bus. Seat screens are reached through
  # --proxy-only, which skips that, and the panel wants it for a11y.
  # 7>&- is load-bearing: dbus-daemon --fork daemonizes while inheriting fd 7,
  # and the daemonized process would keep holding this seat's flock forever —
  # every later claim of the seat queued until its Exec timed out, and the
  # session's desktop could never reconnect. Same lesson as spawn() below.
  if [ "$RUN_DIR" = /run/desktop ] && [ ! -S /run/dbus/system_bus_socket ]; then
    mkdir -p /run/dbus 2>/dev/null || true
    dbus-daemon --system --fork >/dev/null 2>&1 7>&- 8>&- 9>&- || true
  fi
  ensure_desktop "$n" || return 1
  quiet_session "$n"
  if ! port_open "$port"; then
    vnc_args=(-display ":$n" -localhost -rfbport "$port" -nopw -forever -shared -wait 50 -noxdamage -repeat)
    [ "$VIEWONLY" = 1 ] && vnc_args+=(-viewonly)
    spawn "$n" x11vnc x11vnc "${vnc_args[@]}"
    wait_for 80 port_open "$port" || { echo "wk-display: x11vnc :$n did not listen" >&2; return 1; }
  fi
  # Write to a temp name and rename: websockify must never see a half-written token file.
  mkdir -p "$TOKENS_DIR"
  printf '%s: 127.0.0.1:%s\n' "$n" "$port" > "$RUN_DIR/.token.$n.tmp" \
    && mv -f "$RUN_DIR/.token.$n.tmp" "$TOKENS_DIR/$n"
}

cmd_up() {
  valid_seat "${1:-}" || { echo "wk-display: bad seat" >&2; return 2; }
  mkdir -p "$RUN_DIR/seat-$1"
  ( exec 7>"$RUN_DIR/seat-$1.lock"; flock 7; up_locked "$1" )
}

# ---- seat table ----
# The whole mapping lives in FILE NAMES: one empty file per held seat, named
# "<N>-<sid>". Occupancy, both directions of the N<->sid mapping and the
# idleness clock (mtime) are that single directory entry — no file body to
# read, no second copy to keep in sync, and one readdir answers either
# direction with zero forks. The seat number is parsed from the HEAD of the
# name ("${name%%-*}") because sids contain hyphens of their own. The format
# is baked into the image together with this script; the table is ephemeral
# machine-local state, so entries of an older format never coexist with it.
seat_file() { echo "$SEATS_DIR/$1-$2"; } # seat_file N sid

seat_entry() { # seat_entry N -> the table entry of seat N, or nothing
  local f
  for f in "$SEATS_DIR"/$1-*; do
    [ -e "$f" ] && { echo "$f"; return 0; }
  done
  return 1
}

find_seat() { # find_seat <sid> -> N; refreshes the seat's idleness clock on hit
  local f name
  for f in "$SEATS_DIR"/*-"$1"; do
    [ -e "$f" ] || return 1
    # -c: never recreate an entry a concurrent release just removed.
    touch -c "$f" 2>/dev/null
    name=${f##*/}; echo "${name%%-*}"
    return 0
  done
  return 1
}

first_free() { # one readdir beats one stat per seat
  local f name taken=" "
  for f in "$SEATS_DIR"/*-*; do
    [ -e "$f" ] || break
    name=${f##*/}; taken="$taken ${name%%-*} "
  done
  local n
  for ((n = 1; n <= SEAT_MAX; n++)); do
    case "$taken" in *" $n "*) continue ;; esac
    echo "$n"; return 0
  done
  return 1
}

seat_age() {
  local f; f=$(seat_entry "$1") || { echo 0; return; }
  echo $(( $(date +%s) - $(stat -c %Y "$f" 2>/dev/null || date +%s) ))
}

# Called with the global lock held. Idle = old enough AND nothing but the stack on the screen
# AND no VNC viewer connected.
reap_locked() {
  local n f count=0
  for ((n = 1; n <= SEAT_MAX; n++)); do
    f=$(seat_entry "$n") || continue
    [ "$(seat_age "$n")" -gt "$SEAT_IDLE" ] || continue
    seat_busy "$n" && continue
    [ "$(viewers $((VNC_BASE + n)))" -eq 0 ] || continue
    rm -f -- "$f"; kill_seat "$n"; count=$((count + 1))
  done
  echo "$count"
}

# Called with the global lock held, when the table is full and reap_locked
# freed nothing. Takes the single best victim, ranked by:
#   1. no VNC viewer — a hard floor: a watched seat is never taken;
#   2. no application on the screen;
#   3. longest idle.
# Prints the evicted seat, or fails when every seat has a viewer.
evict_locked() {
  local n f rank best="" best_rank=""
  for ((n = 1; n <= SEAT_MAX; n++)); do
    f=$(seat_entry "$n") || continue
    [ "$(viewers $((VNC_BASE + n)))" -eq 0 ] || continue
    if seat_busy "$n"; then rank=1; else rank=2; fi
    rank="$rank:$(printf '%012d' "$(seat_age "$n")")"
    if [ -z "$best" ] || [[ "$rank" > "$best_rank" ]]; then best=$n; best_rank=$rank; fi
  done
  [ -n "$best" ] || return 1
  rm -f -- "$(seat_entry "$best")"; kill_seat "$best"
  echo "$best"
}

alloc_seat() { # global flock protects allocation and any idle-seat reaping
  mkdir -p "$SEATS_DIR" && chmod 0700 "$SEATS_DIR"
  (
    exec 8>"$SEATS_DIR/.lock"; flock 8
    n=$(find_seat "$1") && { echo "$n"; exit 0; } # find_seat refreshed the idleness clock
    n=$(first_free) || { reap_locked >/dev/null; n=$(first_free); } \
      || { evict_locked >/dev/null; n=$(first_free); } || exit 1
    : > "$(seat_file "$n" "$1")"; echo "$n"
  )
}

cmd_claim() {
  valid_sid "${1:-}" || { echo "wk-display: bad session id" >&2; return 2; }
  local n
  n=$(alloc_seat "$1") || { echo WEKNORA_NO_FREE_SCREEN >&2; return 1; }
  cmd_up "$n" || return 1
  echo "$n"
}

cmd_where() { valid_sid "${1:-}" || return 2; find_seat "$1"; }

cmd_sid_of() {
  valid_seat "${1:-}" || return 2
  local f
  f=$(seat_entry "$1") || return 1
  local sid; sid=${f##*/}; sid=${sid#*-}
  [ -n "$sid" ] || return 1
  echo "$sid"
}

cmd_release() {
  valid_sid "${1:-}" || return 2
  mkdir -p "$SEATS_DIR"
  (
    exec 8>"$SEATS_DIR/.lock"; flock 8
    n=$(find_seat "$1") || { echo "no seat held by $1"; exit 0; }
    rm -f -- "$(seat_file "$n" "$1")"; kill_seat "$n"; echo "released seat $n"
  )
}

cmd_reap() {
  mkdir -p "$SEATS_DIR"
  ( exec 8>"$SEATS_DIR/.lock"; flock 8; reap_locked )
}

cmd_list() {
  local n f sid busy
  echo "seats: max=$SEAT_MAX idle=${SEAT_IDLE}s screen=$SCREEN"
  for ((n = 1; n <= SEAT_MAX; n++)); do
    f=$(seat_entry "$n") || continue
    sid=${f##*/}; sid=${sid#*-}; [ -n "$sid" ] || continue
    seat_busy "$n" && busy=busy || busy=idle
    printf '  seat %d | %s | age %ss | %s | viewers %s | vnc %s\n' "$n" "$sid" "$(seat_age "$n")" \
      "$busy" "$(viewers $((VNC_BASE + n)))" "$(port_open $((VNC_BASE + n)) && echo up || echo down)"
  done
}

case "${1:-list}" in
claim)   shift; cmd_claim "$@" ;;
where)   shift; cmd_where "$@" ;;
sid-of)  shift; cmd_sid_of "$@" ;;
release) shift; cmd_release "$@" ;;
reap)    cmd_reap ;;
up)      shift; cmd_up "$@" ;;
list)    cmd_list ;;
*) echo "usage: wk-display {claim <sid>|where <sid>|sid-of <N>|release <sid>|reap|up <N>|list}" >&2; exit 64 ;;
esac

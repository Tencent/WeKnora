#!/usr/bin/env bash
# Session-isolated agent-browser entry. Restore the machine's login state into
# a starting browser before navigation; capture it before closing the browser.
set -u

SID="${WEKNORA_SESSION_ID:-}"
BROWSER_ROOT="${WEKNORA_BROWSER_ROOT:-}"
if [ -z "$SID" ] || [ -z "$BROWSER_ROOT" ]; then
  echo "agent-browser: WEKNORA_SESSION_ID / WEKNORA_BROWSER_ROOT not set; refusing to run unisolated" >&2
  exit 70
fi
AB_REAL="${WK_AB_REAL:-/opt/agent-browser/bin/agent-browser}"
WK_DISPLAY="${WK_DISPLAY:-/usr/local/bin/wk-display}"
SYNC_CMD="${WK_SYNC_CMD:-/usr/local/bin/wk-browser-sync}"
GOVERNOR_CMD="${WK_GOVERNOR_CMD:-/usr/local/bin/wk-ua-governor}"
GOVERNOR_WAIT_S="${WK_GOVERNOR_WAIT_S:-5}"
CHROME="${WK_CHROME:-/usr/local/bin/chrome-wk}"
LINK_CHROME="${WK_LINK_CHROME:-/usr/local/bin/link-chrome-session}"
IDLE_MS="${WK_IDLE_MS:-3600000}"
KEEPALIVE_S="${WK_KEEPALIVE_S:-300}"
LOCK_WAIT_S="${WK_LOCK_WAIT_S:-30}"
CGROUP_DIR="${WK_CGROUP_DIR:-/sys/fs/cgroup}"
SELF="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
# Output and network switches only; every other AGENT_BROWSER_* variable could
# move the daemon, the profile or the browser away from this session's.
AB_ENV_KEEP=" AGENT_BROWSER_JSON AGENT_BROWSER_DEBUG AGENT_BROWSER_DEFAULT_TIMEOUT \
AGENT_BROWSER_MAX_OUTPUT AGENT_BROWSER_CONTENT_BOUNDARIES AGENT_BROWSER_ANNOTATE \
AGENT_BROWSER_SCREENSHOT_DIR AGENT_BROWSER_SCREENSHOT_FORMAT AGENT_BROWSER_SCREENSHOT_QUALITY \
AGENT_BROWSER_COLOR_SCHEME AGENT_BROWSER_DOWNLOAD_PATH AGENT_BROWSER_PROXY \
AGENT_BROWSER_PROXY_BYPASS AGENT_BROWSER_IGNORE_HTTPS_ERRORS "

KEEPALIVE_MODE=0
if [ "${1:-}" = __wk_keepalive ]; then KEEPALIVE_MODE=1; shift; fi

# Discard caller overrides that could escape the session's browser/profile.
SUBCMD_NAV=0; CLOSE_SEEN=0; HEADED_REQUESTED=0; HEADED_FLAG_SEEN=0
SUBCMD=""
CLEAN=()
seen_cmd=0
while [ $# -gt 0 ]; do
  case "$1" in
    --session|--profile|--cdp|--state|--executable-path|-p|--provider)
      shift
      if [ $# -gt 0 ]; then
        case "$1" in --*) ;; *) shift ;; esac
      fi ;;
    --session=*|--profile=*|--cdp=*|--state=*|--executable-path=*|--provider=*|--auto-connect|--auto-connect=*)
      shift ;;
    # Values here may themselves start with "--" (--args "--flag,...").
    --namespace|--config|--args|--extension|--init-script)
      shift
      [ $# -gt 0 ] && shift ;;
    --namespace=*|--config=*|--args=*|--extension=*|--init-script=*)
      shift ;;
    --engine|--idle-timeout)
      CLEAN+=("$1"); shift
      if [ $# -gt 0 ]; then CLEAN+=("$1"); shift; fi ;;
    --headed)
      HEADED_REQUESTED=1; HEADED_FLAG_SEEN=1
      CLEAN+=("$1"); shift
      if [ $# -gt 0 ]; then
        case "$1" in
          true|false)
            [ "$1" = false ] && HEADED_REQUESTED=0
            CLEAN+=("$1"); shift ;;
        esac
      fi ;;
    --headed=false)
      HEADED_REQUESTED=0; HEADED_FLAG_SEEN=1; CLEAN+=("$1"); shift ;;
    --headed=*)
      HEADED_REQUESTED=1; HEADED_FLAG_SEEN=1; CLEAN+=("$1"); shift ;;
    *)
      if [ "$seen_cmd" = 0 ]; then
        case "$1" in
          -*) ;;
          open|goto|navigate) SUBCMD_NAV=1; seen_cmd=1; SUBCMD="$1" ;;
          close|quit|exit) CLOSE_SEEN=1; seen_cmd=1; SUBCMD="$1" ;;
          *) seen_cmd=1; SUBCMD="$1" ;;
        esac
      fi
      CLEAN+=("$1"); shift ;;
  esac
done

if [ "$SUBCMD" = connect ]; then
  echo "agent-browser: connect is disabled; the sandbox manages this session's browser" >&2
  exit 64
fi

mkdir -p "$BROWSER_ROOT/home" "$BROWSER_ROOT/profile"
# Pinning the config file keeps ./agent-browser.json in the workspace and any
# user-level config from reconfiguring the session's daemon.
AB_CONFIG="$BROWSER_ROOT/home/agent-browser-config.json"
printf '{}\n' > "$AB_CONFIG"

# The main Chrome process has our user-data-dir and no child --type flag.
browser_main_pid() {
  local p
  for p in $(pgrep -f -- "--user-data-dir=$BROWSER_ROOT/profile" 2>/dev/null); do
    tr '\0' ' ' < "/proc/$p/cmdline" 2>/dev/null | grep -q -- '--type=' || { echo "$p"; return 0; }
  done
  return 1
}

daemon_alive() {
  local pid
  pid=$(cat "$BROWSER_ROOT/home/.agent-browser/main.pid" 2>/dev/null) || return 1
  case "$pid" in ''|*[!0-9]*) return 1 ;; esac
  kill -0 "$pid" 2>/dev/null
}

# A browser whose daemon died (OOM, a stray kill) keeps the profile lock, and
# every daemon started for this session would fail on it. Save its login
# state and stop it so this command starts a fresh one. Runs before the
# headed decision below, which must see the browser as gone.
if [ "$KEEPALIVE_MODE" = 0 ] && orphan=$(browser_main_pid) && ! daemon_alive; then
  echo "[wrapper] the browser lost its controller; saving its login state and restarting it" >&2
  timeout 10 "$SYNC_CMD" --capture "$BROWSER_ROOT" >/dev/null 2>&1 || true
  kill -TERM "$orphan" 2>/dev/null
  for _ in $(seq 1 50); do kill -0 "$orphan" 2>/dev/null || break; sleep 0.1; done
  kill -KILL "$orphan" 2>/dev/null || true
fi

# A browser this command starts opens on the session's screen unless the
# caller asked for headless: a human clicking the desktop Chrome icon can only
# join a browser that has a window. Commands that start no browser, and
# commands sent to a running one, claim nothing.
if [ "$HEADED_FLAG_SEEN" = 0 ] && ! browser_main_pid >/dev/null 2>&1; then
  case "$SUBCMD" in
    ""|close|quit|exit|help|skills|session|install|upgrade|doctor|profiles|dashboard|plugin|mcp) ;;
    *) HEADED_REQUESTED=1; CLEAN=(--headed "${CLEAN[@]}") ;;
  esac
fi

# Close looks up an existing seat; it never allocates or releases one.
HEADLESS=1
SEAT=""
if [ "$HEADED_REQUESTED" = 1 ]; then
  errf="$BROWSER_ROOT/.claim.err"
  if [ "$CLOSE_SEEN" = 0 ]; then
    SEAT=$("$WK_DISPLAY" claim "$SID" 2>"$errf") || SEAT=""
  else
    SEAT=$("$WK_DISPLAY" where "$SID" 2>"$errf") || SEAT=""
  fi
  case "$SEAT" in
    ""|*[!0-9]*)
      if [ "$CLOSE_SEEN" = 0 ]; then
        echo "[wrapper] no free screen; running headless" >&2
        [ -s "$errf" ] && cat "$errf" >&2
      fi
      KEPT=()
      skip_boolean=0
      for a in "${CLEAN[@]}"; do
        if [ "$skip_boolean" = 1 ]; then
          skip_boolean=0
          case "$a" in true|false) continue ;; esac
        fi
        case "$a" in
          --headed) skip_boolean=1 ;;
          --headed=*) ;;
          *) KEPT+=("$a") ;;
        esac
      done
      CLEAN=("${KEPT[@]}"); SEAT="" ;;
    *) HEADLESS=0 ;;
  esac
  rm -f "$errf"
fi

# The CLI prefers $XDG_RUNTIME_DIR for its daemon socket. A desktop click
# inherits the seat's XDG_RUNTIME_DIR and the agent's exec has none; without a
# pinned socket dir they start two daemons that fight over one profile lock.
ab() {
  local disp="" v
  local unset_env=()
  [ "$HEADLESS" = 0 ] && disp=":$SEAT"
  for v in $(compgen -e); do
    case "$v" in
      AGENT_BROWSER_*) case "$AB_ENV_KEEP" in *" $v "*) ;; *) unset_env+=(-u "$v") ;; esac ;;
    esac
  done
  env "${unset_env[@]}" HOME="$BROWSER_ROOT/home" DISPLAY="$disp" WK_SEAT="$SEAT" \
    AGENT_BROWSER_CONFIG="$AB_CONFIG" \
    AGENT_BROWSER_SOCKET_DIR="$BROWSER_ROOT/home/.agent-browser" \
    AGENT_BROWSER_EXECUTABLE_PATH="$CHROME" AGENT_BROWSER_SESSION=main \
    AGENT_BROWSER_IDLE_TIMEOUT_MS="$IDLE_MS" \
    "$AB_REAL" --profile "$BROWSER_ROOT/profile" "$@"
}

# The daemon's idle timeout counts only CLI commands, not a human working in
# the window. While someone watches the session's screen, a read-only command
# every KEEPALIVE_S keeps the browser open; unwatched, it closes on schedule.
keepalive() {
  exec 9>"$BROWSER_ROOT/.keepalive.lock"
  flock -n 9 || return 0
  while sleep "$KEEPALIVE_S" && browser_main_pid >/dev/null 2>&1; do
    "$WK_DISPLAY" watched "$SID" >/dev/null 2>&1 || continue
    # Re-checked under the lock: a command sent to a closed daemon relaunches it.
    ( flock -n 7 || exit 0; browser_main_pid >/dev/null 2>&1 || exit 0; ab get url >/dev/null 2>&1 ) \
      7>"$BROWSER_ROOT/.command.lock" 9>&-
  done
}
if [ "$KEEPALIVE_MODE" = 1 ]; then
  keepalive
  exit 0
fi

# A browser that cannot fork or is OOM-killed while starting only surfaces in
# the CLI as a read timeout ("Resource temporarily unavailable"). The cgroup
# event counters tell whether the sandbox's limits were the cause.
cg_event() { # cg_event <file> <key> -> counter, empty when unreadable
  awk -v k="$2" '$1 == k { print $2 }' "$CGROUP_DIR/$1" 2>/dev/null
}
cg_value() { cat "$CGROUP_DIR/$1" 2>/dev/null; }
PIDS_EVENTS_BEFORE=$(cg_event pids.events max)
OOM_KILLS_BEFORE=$(cg_event memory.events oom_kill)
explain_failure() {
  local pids oom max cur
  pids=$(cg_event pids.events max)
  oom=$(cg_event memory.events oom_kill)
  if [ -n "$pids" ] && [ -n "$PIDS_EVENTS_BEFORE" ] && [ "$pids" -gt "$PIDS_EVENTS_BEFORE" ]; then
    echo "agent-browser: the sandbox hit its process limit ($(cg_value pids.current)/$(cg_value pids.max) processes and threads) while this command ran; a browser needs about 200. Close browsers that are no longer needed (agent-browser close) and retry; if it keeps failing, the sandbox's process limit is too low for a browser." >&2
  fi
  if [ -n "$oom" ] && [ -n "$OOM_KILLS_BEFORE" ] && [ "$oom" -gt "$OOM_KILLS_BEFORE" ]; then
    max=$(cg_value memory.max); cur=$(cg_value memory.current)
    case "$max" in ''|*[!0-9]*) ;; *) max="$((max / 1048576))MiB" ;; esac
    case "$cur" in ''|*[!0-9]*) ;; *) cur="$((cur / 1048576))MiB" ;; esac
    echo "agent-browser: the sandbox ran out of memory ($cur of $max in use) and the kernel killed processes while this command ran. Close browsers that are no longer needed (agent-browser close) and retry; if it keeps failing, the sandbox's memory limit is too low for a browser." >&2
  fi
}

# Serialize commands per session; activity defers receiver reloads.
exec 7>"$BROWSER_ROOT/.command.lock"
if ! flock -w "$LOCK_WAIT_S" 7; then
  echo "agent-browser: another browser command of this session is still running (waited ${LOCK_WAIT_S}s); retry when it finishes" >&2
  exit 70
fi
touch "$BROWSER_ROOT/.busy"
cleanup() { rm -f "$BROWSER_ROOT/.busy" "$BROWSER_ROOT/.launching"; }
trap cleanup EXIT
# Refresh the busy TTL for commands longer than one mirror interval.
(
  while kill -0 "$$" 2>/dev/null; do
    touch -c "$BROWSER_ROOT/.busy"
    sleep 10
  done
) </dev/null >/dev/null 2>&1 7>&- &
busy_pid=$!
trap 'kill "$busy_pid" 2>/dev/null || true; cleanup' EXIT

# Capture while the browser is live. Failure must not prevent closing it.
HUMAN_TAB="$BROWSER_ROOT/.human-tab"
[ "$CLOSE_SEEN" = 1 ] && rm -f "$HUMAN_TAB"
if [ "$CLOSE_SEEN" = 1 ] && browser_main_pid >/dev/null 2>&1; then
  timeout 10 "$SYNC_CMD" --capture "$BROWSER_ROOT" >/dev/null 2>&1 7>&- || \
    echo "[wrapper] pre-close capture failed; keeping the saved login state" >&2
fi

if [ "$SUBCMD_NAV" = 1 ]; then
  if [ ! -x "$SYNC_CMD" ]; then
    echo "agent-browser: browser state sync is unavailable" >&2
    exit 70
  fi
  cold=0
  browser_main_pid >/dev/null 2>&1 || cold=1
  if [ "$cold" = 1 ]; then
    # Link the saved-password databases before Chrome opens them; never replace
    # database paths beneath a running browser.
    if [ -x "$LINK_CHROME" ]; then
      "$LINK_CHROME" "$BROWSER_ROOT/profile" 7>&- || exit 70
    fi
    touch "$BROWSER_ROOT/.launching"
    rm -f "$BROWSER_ROOT/.ua-governed" "$HUMAN_TAB"
  fi
  # Machine daemons get their own session: cancelling the command that started
  # them signals its whole process group. The governor exits at once when a
  # copy is already running, so every navigation restarts a killed one.
  if [ -x "$GOVERNOR_CMD" ]; then
    (cd / && exec setsid "$GOVERNOR_CMD") >>"$BROWSER_ROOT/.governor.log" 2>&1 </dev/null 7>&- &
  fi
  if [ "$cold" = 1 ]; then
    boot=()
    [ "$HEADLESS" = 0 ] && boot+=(--headed)
    ab "${boot[@]}" open about:blank >/dev/null 7>&-
    rc=$?
    if [ "$rc" -ne 0 ]; then explain_failure; exit "$rc"; fi
    timeout 15 "$SYNC_CMD" --restore-root "$BROWSER_ROOT" 7>&- || \
      echo "[wrapper] login state restore incomplete; proceeding without it" >&2
    # The first real page must load with the governed identity.
    if [ -x "$GOVERNOR_CMD" ]; then
      for _ in $(seq 1 $((GOVERNOR_WAIT_S * 10))); do
        [ -e "$BROWSER_ROOT/.ua-governed" ] && break
        sleep 0.1
      done
      [ -e "$BROWSER_ROOT/.ua-governed" ] || \
        echo "[wrapper] fingerprint governor not attached; proceeding ungoverned" >&2
    fi
    rm -f "$BROWSER_ROOT/.launching"
  elif [ "${WK_DESKTOP_CLICK:-}" = 1 ]; then
    DESKTOP_JOIN=1
  fi
  # The active tab is the human's once a desktop click opened or joined the
  # browser; the agent's next navigation takes a tab of its own instead.
  if [ "${WK_DESKTOP_CLICK:-}" = 1 ]; then
    touch "$HUMAN_TAB"
  elif [ "$cold" = 0 ] && [ -e "$HUMAN_TAB" ]; then
    rm -f "$HUMAN_TAB"
    AGENT_NEW_TAB=1
  fi
  # Neutral cwd/stdio and no command lock inherited by machine daemons.
  (cd / && exec setsid "$SYNC_CMD") >>"$BROWSER_ROOT/.sync.log" 2>&1 </dev/null 7>&- &
  (cd / && exec setsid "$SELF" __wk_keepalive) >/dev/null 2>&1 </dev/null 7>&- &
fi

# A desktop click on a running browser must not navigate the page the agent
# or the human is on: a link gets a tab of its own, a bare icon click only
# brings the window forward.
nav_to_new_tab() {
  local i
  for i in "${!CLEAN[@]}"; do
    case "${CLEAN[$i]}" in
      open|goto|navigate) CLEAN=("${CLEAN[@]:0:i}" tab new "${CLEAN[@]:i+1}"); return ;;
    esac
  done
}
if [ "${DESKTOP_JOIN:-0}" = 1 ]; then
  for i in "${!CLEAN[@]}"; do
    case "${CLEAN[$i]}" in open|goto|navigate) break ;; esac
  done
  url="${CLEAN[$((i + 1))]:-about:blank}"
  if [ "$url" = about:blank ]; then
    if [ -n "$SEAT" ]; then
      DISPLAY=":$SEAT" timeout 3 "${WK_XDOTOOL:-xdotool}" \
        search --class google-chrome windowactivate %@ >/dev/null 2>&1 7>&- || true
    fi
    exit 0
  fi
  nav_to_new_tab
elif [ "${AGENT_NEW_TAB:-0}" = 1 ]; then
  nav_to_new_tab
fi

ab "${CLEAN[@]}" 7>&-
rc=$?
[ "$rc" -ne 0 ] && explain_failure
exit "$rc"

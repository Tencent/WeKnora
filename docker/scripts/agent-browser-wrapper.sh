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
IDLE_MS=3600000

# Discard caller overrides that could escape the session's browser/profile.
SUBCMD_NAV=0; CLOSE_SEEN=0; HEADED_REQUESTED=0
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
    --engine|--idle-timeout)
      CLEAN+=("$1"); shift
      if [ $# -gt 0 ]; then CLEAN+=("$1"); shift; fi ;;
    --headed)
      HEADED_REQUESTED=1
      CLEAN+=("$1"); shift
      if [ $# -gt 0 ]; then
        case "$1" in
          true|false)
            [ "$1" = false ] && HEADED_REQUESTED=0
            CLEAN+=("$1"); shift ;;
        esac
      fi ;;
    --headed=false)
      HEADED_REQUESTED=0; CLEAN+=("$1"); shift ;;
    --headed=*)
      HEADED_REQUESTED=1; CLEAN+=("$1"); shift ;;
    *)
      if [ "$seen_cmd" = 0 ]; then
        case "$1" in
          -*) ;;
          open|goto|navigate) SUBCMD_NAV=1; seen_cmd=1 ;;
          close|quit|exit) CLOSE_SEEN=1; seen_cmd=1 ;;
          *) seen_cmd=1 ;;
        esac
      fi
      CLEAN+=("$1"); shift ;;
  esac
done

mkdir -p "$BROWSER_ROOT/home" "$BROWSER_ROOT/profile"

# The main Chrome process has our user-data-dir and no child --type flag.
browser_main_pid() {
  local p
  for p in $(pgrep -f -- "--user-data-dir=$BROWSER_ROOT/profile" 2>/dev/null); do
    tr '\0' ' ' < "/proc/$p/cmdline" 2>/dev/null | grep -q -- '--type=' || { echo "$p"; return 0; }
  done
  return 1
}

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

ab() {
  local disp=""
  [ "$HEADLESS" = 0 ] && disp=":$SEAT"
  env HOME="$BROWSER_ROOT/home" DISPLAY="$disp" WK_SEAT="$SEAT" \
    AGENT_BROWSER_EXECUTABLE_PATH="$CHROME" AGENT_BROWSER_SESSION=main \
    AGENT_BROWSER_IDLE_TIMEOUT_MS="$IDLE_MS" \
    "$AB_REAL" --profile "$BROWSER_ROOT/profile" "$@"
}

# Serialize commands per session; activity defers receiver reloads.
exec 7>"$BROWSER_ROOT/.command.lock"
flock -w 30 7 || exit 70
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
if [ "$CLOSE_SEEN" = 1 ] && browser_main_pid >/dev/null 2>&1; then
  timeout 10 "$SYNC_CMD" --capture "$BROWSER_ROOT" >/dev/null 2>&1 7>&- || \
    echo "[wrapper] pre-close capture failed; keeping the saved login state" >&2
fi

if [ "$SUBCMD_NAV" = 1 ]; then
  if [ ! -x "$SYNC_CMD" ]; then
    echo "agent-browser: browser state sync is unavailable" >&2
    exit 70
  fi
  if ! browser_main_pid >/dev/null 2>&1; then
    # Link the saved-password databases before Chrome opens them; never replace
    # database paths beneath a running browser.
    if [ -x "$LINK_CHROME" ]; then
      "$LINK_CHROME" "$BROWSER_ROOT/profile" 7>&- || exit 70
    fi
    touch "$BROWSER_ROOT/.launching"
    rm -f "$BROWSER_ROOT/.ua-governed"
    if [ -x "$GOVERNOR_CMD" ]; then
      (cd / && "$GOVERNOR_CMD") >>"$BROWSER_ROOT/.governor.log" 2>&1 </dev/null 7>&- &
    fi
    boot=()
    [ "$HEADLESS" = 0 ] && boot+=(--headed)
    ab "${boot[@]}" open about:blank >/dev/null 7>&- || exit $?
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
  fi
  # Neutral cwd/stdio and no command lock inherited by machine daemons.
  (cd / && "$SYNC_CMD") >>"$BROWSER_ROOT/.sync.log" 2>&1 </dev/null 7>&- &
fi

ab "${CLEAN[@]}" 7>&-
exit $?

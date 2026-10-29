#!/usr/bin/env bash
# Fake agent-browser for wrapper/wk-auth tests.
set -u
STATE="${STUB_STATE:?}"; LOG="${STUB_LOG:?}"
echo "ARGS $*" >> "$LOG"
echo "ENV HOME=$HOME DISPLAY=${DISPLAY-} SESSION=${AGENT_BROWSER_SESSION:-} EXE=${AGENT_BROWSER_EXECUTABLE_PATH:-} SEAT=${WK_SEAT-}" >> "$LOG"

# Build a clean positional array: drop every flag; the value-taking flags
# (--profile/--cdp/--idle-timeout) also drop the value that follows, and
# --profile's value is kept aside for the fake browser marker.
ARGS=()
profile=""
prev=""
for a in "$@"; do
  case "$prev" in
    --profile) profile="$a"; prev=""; continue ;;
    --cdp|--idle-timeout) prev=""; continue ;;
  esac
  case "$a" in
    --profile|--cdp|--idle-timeout) prev="$a"; continue ;;
    --*) prev=""; continue ;;
    *) ARGS+=("$a") ;;
  esac
done
sub="${ARGS[0]:-}"
chrome_alive() { [ -f "$STATE/chrome.pid" ] && kill -0 "$(cat "$STATE/chrome.pid")" 2>/dev/null; }

case "$sub" in
  open|goto|navigate)
    if ! chrome_alive; then
      bash -c "sleep 300; true # --user-data-dir=$profile" >/dev/null 2>&1 &
      echo $! > "$STATE/chrome.pid"
    fi
    exit "${STUB_OPEN_RC:-0}" ;;
  close|quit|exit)
    if chrome_alive; then
      p="$(cat "$STATE/chrome.pid")"
      pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null
      rm -f "$STATE/chrome.pid"
      exit 0
    fi
    exit 1 ;;
  state)
    case "$*" in
      *save*)
        [ -z "${STUB_FAIL:-}" ] || exit 1
        # A real "state save" leaves its daemon running with every fd it
        # inherited, including the vault lock.
        if [ -n "${STUB_LEAK:-}" ]; then sleep 5 >/dev/null 2>&1 & fi
        last="${ARGS[${#ARGS[@]}-1]}"
        if [ -n "${STUB_BAD_JSON:-}" ]; then echo 'not json' > "$last"; else echo '{"cookies":[]}' > "$last"; fi
        if [ -n "${STUB_CAPTURE_FILE:-}" ]; then cp "$STUB_CAPTURE_FILE" "$last"; fi
        exit 0 ;;
    esac ;;
esac
exit 0

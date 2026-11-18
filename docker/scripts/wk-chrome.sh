#!/bin/sh
# wk-chrome: the GUI entry to the session browser. Every graphical way of
# starting Chrome (desktop shortcut, Applications menu, default http/https
# handler) lands here, so a human clicking Chrome opens THIS session's agent
# browser — same profile and daemon, synced by wk-browser-sync —
# instead of an orphan Chrome with its own untracked profile.
#
# Unknown context (no seat, no holding session, :0 legacy desktop) falls back
# to the plain chrome-wk shim: never worse than a direct Chrome start.
#
# A held seat always reaches the session browser, even before the agent has
# opened one: the wrapper creates the root and restores the login state before
# the first page loads. The root is the one the backend exports as
# WEKNORA_BROWSER_ROOT, which wk-display records when the session claims the
# seat.
#
# Paths are WK_*-overridable for the tests, same convention as the wrapper.
set -u

CHROME_WK="${WK_CHROME_SHIM:-/usr/local/bin/chrome-wk}"
DISPLAY_CMD="${WK_DISPLAY:-/usr/local/bin/wk-display}"
AB="${WK_AB_CMD:-/usr/local/bin/agent-browser}"
SESSIONS_ROOT="${WK_SESSIONS_ROOT:-/var/lib/weknora/sessions}"
SINGLE_ROOT="${WK_SINGLE_BROWSER_ROOT:-/var/lib/weknora/browser/session}"

url=about:blank
for a in "$@"; do
  case "$a" in
    http://*|https://*) url="$a"; break ;;
  esac
done

seat="${DISPLAY:-}"
seat="${seat#:}"
seat="${seat%%.*}"
case "$seat" in
  ''|*[!0-9]*|0) exec "$CHROME_WK" "$@" ;;
esac

sid="$("$DISPLAY_CMD" sid-of "$seat" 2>/dev/null)" || exec "$CHROME_WK" "$@"
case "$sid" in
  ''|.*|*[!A-Za-z0-9._-]*) exec "$CHROME_WK" "$@" ;;
esac

# The root recorded when the session claimed this seat is authoritative. The
# guess below only covers seats claimed before claims recorded it: before the
# session's first browser, the sessions directory may not exist yet.
root="$("$DISPLAY_CMD" browser-root "$sid" 2>/dev/null)" || root=""
if [ -z "$root" ]; then
  root="$SESSIONS_ROOT/$sid.browser"
  [ -d "$root" ] || [ -d "$SESSIONS_ROOT" ] || root="$SINGLE_ROOT"
fi

export WEKNORA_SESSION_ID="$sid" WEKNORA_BROWSER_ROOT="$root"
# WK_DESKTOP_CLICK keeps a click from navigating the page of a running browser.
export WK_DESKTOP_CLICK=1
# The wrapper finds the browser, waits for browser sync, and opens the URL on
# this session's screen. A click has no terminal, so a failure is shown there.
"$AB" --headed open "$url" >>/tmp/wk-chrome.log 2>&1 && exit 0
rc=$?
"${WK_NOTIFY:-xmessage}" -center -timeout 15 \
  "The browser did not open (exit $rc). If the assistant is using it, try again in a moment." \
  >/dev/null 2>&1 &
exit "$rc"

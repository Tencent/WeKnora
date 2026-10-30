#!/bin/sh
# Google Chrome refuses to run as root without --no-sandbox, and the agent
# shell is root. /dev/shm is tiny in containers. exec keeps the pid stable so
# the wrapper can find the browser main process by its user-data-dir.
#
# Base flags for launches through this shim (session browsers and GUI fallback):
#   --disable-blink-features=AutomationControlled
#       Under CDP control Chrome would report
#       navigator.webdriver === true; Google sign-in and similar gates read
#       that as automation and refuse the login.
#   --hide-crash-restore-bubble  seat reaping KILLs the browser; the next
#       launch must not greet the human with "Chrome didn't shut down
#       correctly" — it also reads as automated.
#   --no-first-run --no-default-browser-check  no first-run dialogs.
#   --password-store=basic  there is no keyring in the image.
#   --enable-unsafe-swiftshader  no GPU here; a broken WebGL context is its
#       own bot tell, working software WebGL is not.
#   --accept-lang=en-US,en  the language list wk-ua-governor also reports, so
#       the first request of a page agrees with what scripts see.
#
# $BASE below is expanded unquoted on purpose: the flags are plain words and
# must split.
#
# A seat screen (WK_SEAT set) is a fixed framebuffer. Place the window at the
# origin and size it to that screen. A launch without WK_SEAT (headless, or
# the shared :0 desktop) keeps whatever size it was given.
#
# wk-machine-id pins the device identity across container rebuilds before any
# Chrome reads it. Guarded so the shim still works when the script is absent
# (tests, non-desktop images).
MACHINE_ID="${WK_MACHINE_ID_SCRIPT:-/usr/local/bin/wk-machine-id}"
[ -x "$MACHINE_ID" ] && "$MACHINE_ID" 2>/dev/null || true

BIN="${WK_CHROME_REAL:-/usr/bin/google-chrome}"
geom="${WK_SCREEN:-1280x800x24}"
w=$(printf '%s' "$geom" | cut -dx -f1)
h=$(printf '%s' "$geom" | cut -dx -f2)
case "$w" in ""|*[!0-9]*) w=1280 ;; esac
case "$h" in ""|*[!0-9]*) h=800 ;; esac

BASE="--no-sandbox --disable-dev-shm-usage \
--disable-blink-features=AutomationControlled --hide-crash-restore-bubble \
--no-first-run --no-default-browser-check --password-store=basic \
--enable-unsafe-swiftshader --accept-lang=en-US,en"

if [ -n "${WK_SEAT:-}" ]; then
  exec "$BIN" $BASE --window-position=0,0 --window-size="${w},${h}" "$@"
fi
exec "$BIN" $BASE "$@"

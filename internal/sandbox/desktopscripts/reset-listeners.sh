#!/bin/sh
# Tear down a half-started desktop so the next start-desktop.sh rebuilds it.
#
# Xvfb and the XFCE session are deliberately left alone: they are expensive to
# restart and never the reason 6080 is mute.
#
# Sent over Exec by SandboxDesktopService, not baked into the image, so it
# also applies to sandboxes running an older desktop image.
set -u

pkill -x x11vnc >/dev/null 2>&1 || true
pkill -f '[w]ebsockify.*6080' >/dev/null 2>&1 || true

# Images built before the 9>&- fix leak the flock fd into every daemon the
# script spawns, so killing x11vnc/websockify leaves Xvfb and xfce4-session
# holding the lock and the next start-desktop.sh blocks on flock until its
# Exec times out (exit=-1, 6080 never returns). Unlinking the lock file gives
# that run a fresh inode to lock. Safe here: the per-sandbox secret the lock
# protects is written once and already exists by the time anything is reset.
if [ -e /run/desktop/.lock ]; then
    for holder in /proc/[0-9]*/fd/*; do
        [ -e "$holder" ] || continue
        case "$(readlink "$holder" 2>/dev/null)" in
            */run/desktop/.lock)
                rm -f /run/desktop/.lock
                break
                ;;
        esac
    done
fi

exit 0

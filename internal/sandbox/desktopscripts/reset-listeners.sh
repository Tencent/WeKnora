#!/bin/sh
# Tear down a half-started desktop so the next start-desktop.sh rebuilds it.
#
# Xvfb and the XFCE session are deliberately left alone: they are expensive to
# restart and never the reason 6080 is mute.
#
# Sent over Exec by SandboxDesktopService, not baked into the image, so it
# also applies to sandboxes running an older desktop image.
set -u

# Only the shared :0 server. Seat VNC uses -rfbport 5901 and up; a bare
# `pkill -x x11vnc` would take those down while another session is watching.
# [x] keeps pgrep from matching this script's own command line.
pkill -f '[x]11vnc.*-rfbport 5900([^0-9]|$)' >/dev/null 2>&1 || true
pkill -f '[w]ebsockify.*6080' >/dev/null 2>&1 || true

# Images built before the fd-close fixes leak the flock fd into daemons they
# spawn (start-desktop.sh leaked fd 9; wk-display.sh's dbus-daemon --system
# held a seat's fd 7 for the daemon's lifetime), so the next start or claim
# blocks on flock until its Exec times out. Unlinking a lock file that some
# process still holds gives the next run a fresh inode to lock. Safe here:
# the per-sandbox secret the .lock protects is written once and already
# exists by the time anything is reset, and the seat stack steps are
# check-then-act idempotent, so a claim racing the unlink only ever loses a
# bind and retries.
for lockfile in /run/desktop/.lock /run/desktop/seat-*.lock; do
    [ -e "$lockfile" ] || continue
    for holder in /proc/[0-9]*/fd/*; do
        [ -e "$holder" ] || continue
        case "$(readlink "$holder" 2>/dev/null)" in
            *"$lockfile")
                rm -f "$lockfile"
                break
                ;;
        esac
    done
done

exit 0

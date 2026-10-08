#!/bin/sh
# One Exec: prove the image has a desktop, start it, print the websockify secret.
#
# exit 2  — start-desktop.sh missing. stderr MUST contain
#            WEKNORA_DESKTOP_UNSUPPORTED; POSIX sh also uses 2 for
#            syntax errors, so Go ignores a bare 2.
# exit 1  — start failed or listeners never came up
# exit 0  — stdout is exactly one line: READY <32 alnum>
#
# Daemons must not inherit this Exec's pipes (envd waits for them to close)
# or fd 9 (see start-desktop.sh). Reset/retry lives in Go, not here: a 60s
# timeout kill cannot run the rest of this script.
set -u

if [ ! -x /usr/local/bin/start-desktop.sh ]; then
  echo WEKNORA_DESKTOP_UNSUPPORTED >&2
  exit 2
fi

export LANG="${LANG:-C.UTF-8}"
export LC_ALL="${LC_ALL:-C.UTF-8}"

mkdir -p /run/desktop
log=/run/desktop/start-desktop.log

/usr/local/bin/start-desktop.sh </dev/null >>"$log" 2>&1
rc=$?
if [ "$rc" -ne 0 ]; then
  tail -c 2000 "$log" >&2
  exit 1
fi

listening() {
    hex=$(printf '%04X' "$1")
    cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk -v hex="$hex" '
        $4 == "0A" {
            n = split($2, addr, ":")
            if (toupper(addr[n]) == hex) { found = 1 }
        }
        END { exit !found }
    '
}

if ! listening 5900 || ! listening 6080; then
  tail -c 2000 "$log" >&2
  exit 1
fi

secret=$(cat /run/desktop/secret 2>/dev/null) || true
case "$secret" in
  *[!A-Za-z0-9]*|"")
    echo "desktop secret missing or malformed" >&2
    exit 1
    ;;
esac
# start-desktop.sh writes 32 bytes; reject anything else so Go can require
# ^READY ([A-Za-z0-9]{32})$ without a second round trip.
if [ "${#secret}" -ne 32 ]; then
  echo "desktop secret length ${#secret}" >&2
  exit 1
fi

printf 'READY %s\n' "$secret"

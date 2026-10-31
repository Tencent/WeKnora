#!/bin/sh
# One Exec: prove the image has a desktop, start it, print the websockify secret.
#
# exit 2  — start-desktop.sh missing. stderr MUST contain
#            WEKNORA_DESKTOP_UNSUPPORTED; POSIX sh also uses 2 for
#            syntax errors, so Go ignores a bare 2.
# exit 1  — start failed, listeners never came up, or no free seat.
#            A full seat table also writes WEKNORA_NO_FREE_SCREEN.
# exit 0  — stdout is exactly one line:
#            READY <32 alnum>            legacy image, shared :0
#            READY <32 alnum> 0          new image, shared :0
#            READY <32 alnum> <seat>     this session's own screen
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

read_secret() {
  secret=$(cat /run/desktop/secret 2>/dev/null) || true
  case "$secret" in
    *[!A-Za-z0-9]*|"")
      echo "desktop secret missing or malformed" >&2
      return 1
      ;;
  esac
  # start-desktop.sh writes 32 bytes; reject anything else so Go can require
  # ^READY ([A-Za-z0-9]{32})$ without a second round trip.
  if [ "${#secret}" -ne 32 ]; then
    echo "desktop secret length ${#secret}" >&2
    return 1
  fi
  return 0
}

# Seat path. The session id is injected by the backend; a value the agent
# put in the command cannot override it. Images without wk-display, and
# execs with no session id, keep the shared :0 desktop.
if [ -n "${WEKNORA_SESSION_ID:-}" ] && command -v wk-display >/dev/null 2>&1; then
  /usr/local/bin/start-desktop.sh --proxy-only </dev/null >>"$log" 2>&1
  rc=$?
  if [ "$rc" -ne 0 ]; then
    tail -c 2000 "$log" >&2
    exit 1
  fi
  if ! listening 6080; then
    tail -c 2000 "$log" >&2
    exit 1
  fi
  read_secret || exit 1
  # claim prints only the seat number. Its stderr (including
  # WEKNORA_NO_FREE_SCREEN) must reach the caller, not stdout.
  seat=$(wk-display claim "$WEKNORA_SESSION_ID" 2>>"$log")
  rc=$?
  if [ "$rc" -ne 0 ]; then
    tail -c 2000 "$log" >&2
    exit 1
  fi
  case "$seat" in
    ""|*[!0-9]*)
      echo "wk-display claim returned unexpected seat" >&2
      exit 1
      ;;
  esac
  port=$((5900 + seat))
  if ! listening "$port"; then
    echo "seat $seat vnc not listening on $port" >&2
    exit 1
  fi
  printf 'READY %s %s\n' "$secret" "$seat"
  exit 0
fi

/usr/local/bin/start-desktop.sh </dev/null >>"$log" 2>&1
rc=$?
if [ "$rc" -ne 0 ]; then
  tail -c 2000 "$log" >&2
  exit 1
fi

if ! listening 5900 || ! listening 6080; then
  tail -c 2000 "$log" >&2
  exit 1
fi

read_secret || exit 1

# Token 0 means "shared :0", not a seat. Old images have no token file and
# print the legacy line so an old backend still dials the bare path.
if [ -s /run/desktop/tokens.d/0 ]; then
  printf 'READY %s 0\n' "$secret"
else
  printf 'READY %s\n' "$secret"
fi

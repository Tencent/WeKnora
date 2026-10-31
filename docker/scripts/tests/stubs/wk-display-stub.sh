#!/usr/bin/env bash
# Fixed-seat stand-in for wrapper tests. Records every invocation.
# Same session id keeps the same number. A full table exits 1.
set -u
DIR="${WK_DISPLAY_STATE:?}"
LOG="${WK_DISPLAY_LOG:-$DIR/log}"
MAX="${WK_DISPLAY_MAX:-8}"
mkdir -p "$DIR/seats"
echo "$*" >> "$LOG"
cmd="${1:-}"
sid="${2:-}"
case "$cmd" in
  claim|where)
    n=1
    while [ "$n" -le "$MAX" ]; do
      if [ "$(cat "$DIR/seats/$n" 2>/dev/null)" = "$sid" ]; then
        echo "$n"
        exit 0
      fi
      n=$((n + 1))
    done
    [ "$cmd" = where ] && exit 1
    n=1
    while [ "$n" -le "$MAX" ]; do
      if [ ! -s "$DIR/seats/$n" ]; then
        printf '%s\n' "$sid" > "$DIR/seats/$n"
        echo "$n"
        exit 0
      fi
      n=$((n + 1))
    done
    echo WEKNORA_NO_FREE_SCREEN >&2
    exit 1
    ;;
  release)
    exit 0
    ;;
  *)
    echo "usage" >&2
    exit 64
    ;;
esac

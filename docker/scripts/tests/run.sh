#!/usr/bin/env bash
# Syntax-check every script, then run each test_*.sh. Usage: docker/scripts/tests/run.sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPTS="$HERE/.."
rc=0
for f in "$SCRIPTS"/*.sh; do
  first="$(head -1 "$f")"
  case "$first" in
    *bash*) bash -n "$f" || { echo "SYNTAX $f"; rc=1; } ;;
    *) sh -n "$f" || { echo "SYNTAX $f"; rc=1; } ;;
  esac
done
for t in "$HERE"/test_*.sh; do
  echo "== $(basename "$t")"
  bash "$t" || rc=1
done
exit $rc

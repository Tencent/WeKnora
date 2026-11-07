#!/usr/bin/env bash
# Tiny assertion helpers; sourced by every test_*.sh.
set -u
TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ORIG_PATH="$PATH"
FAILS=0

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
assert_eq() { if [ "$2" = "$3" ]; then pass "$1"; else fail "$1: want [$2] got [$3]"; fi; }
assert_contains() { case "$3" in *"$2"*) pass "$1" ;; *) fail "$1: [$3] lacks [$2]" ;; esac; }
assert_not_contains() { case "$3" in *"$2"*) fail "$1: [$3] must not contain [$2]" ;; *) pass "$1" ;; esac; }
finish() { [ "$FAILS" -eq 0 ]; }

teardown() {
  if [ -f "${STUB_STATE:-/nonexistent}/chrome.pid" ]; then
    local p; p="$(cat "$STUB_STATE/chrome.pid")"
    pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null
  fi
  export PATH="$ORIG_PATH"
  rm -rf "$TMP"
}

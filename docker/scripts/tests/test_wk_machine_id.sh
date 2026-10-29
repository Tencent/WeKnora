#!/usr/bin/env bash
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
MID="$HERE/../wk-machine-id.sh"

setup_mid() {
  TMP="$(mktemp -d)"; export TMP
  export WK_MACHINE_ID="$TMP/state/machine-id" WK_MACHINE_ID_ETC="$TMP/etc/machine-id"
  mkdir -p "$TMP/state" "$(dirname "$WK_MACHINE_ID_ETC")"
  : > "$WK_MACHINE_ID_ETC"
  unset WK_DBUS_UUIDGEN
}

t_first_run_adopts_the_live_id() {
  setup_mid
  printf '0123456789abcdef0123456789abcdef\n' > "$WK_MACHINE_ID_ETC"
  "$MID"
  assert_eq "persist adopts the live id" "0123456789abcdef0123456789abcdef" "$(cat "$WK_MACHINE_ID")"
  assert_eq "second run is a no-op write-back" 0 "$("$MID"; echo $?)"
  teardown
}

t_first_run_mints_one_when_etc_is_junk() {
  setup_mid
  printf 'not-an-id\n' > "$WK_MACHINE_ID_ETC"
  # A minting source we control beats depending on dbus-uuidgen in the test env.
  mkdir -p "$TMP/bin"
  printf '#!/bin/sh\necho 99999999999999999999999999999999\n' > "$TMP/bin/dbus-uuidgen"
  chmod 0755 "$TMP/bin/dbus-uuidgen"
  PATH="$TMP/bin:$PATH" "$MID"
  assert_eq "minted id persisted" "99999999999999999999999999999999" "$(cat "$WK_MACHINE_ID")"
  teardown
}

t_rebuild_gets_the_old_identity_written_back() {
  setup_mid
  printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' > "$WK_MACHINE_ID"
  printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' > "$WK_MACHINE_ID_ETC"   # fresh container layer
  "$MID"
  assert_eq "/etc machine-id restored from persist" "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" "$(cat "$WK_MACHINE_ID_ETC")"
  teardown
}

t_first_run_adopts_the_live_id
t_first_run_mints_one_when_etc_is_junk
t_rebuild_gets_the_old_identity_written_back
finish

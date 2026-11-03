#!/usr/bin/env bash
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export WK_CHROME_SHARED="$TMP/shared"
for profile in "$TMP/a" "$TMP/b"; do
  bash "$HERE/../link-chrome-session.sh" "$profile"
  for name in 'Login Data' 'Login Data For Account'; do
    test "$(readlink "$profile/Default/$name")" = "$TMP/shared/Default/$name"
  done
  test ! -e "$profile/Default/Cookies"
done
printf '%s' shared > "$TMP/shared/Default/Login Data"
test "$(cat "$TMP/a/Default/Login Data")" = shared
test "$(cat "$TMP/b/Default/Login Data")" = shared

# A profile prepared by the old script had a shared Cookies link; it goes away.
ln -s "$TMP/shared/Default/Cookies" "$TMP/a/Default/Cookies"
bash "$HERE/../link-chrome-session.sh" "$TMP/a"
test ! -L "$TMP/a/Default/Cookies"
# A private Cookies file is never touched.
printf '%s' private > "$TMP/b/Default/Cookies"
bash "$HERE/../link-chrome-session.sh" "$TMP/b"
test "$(cat "$TMP/b/Default/Cookies")" = private
printf '%s\n' 'ok saved passwords are shared, cookies stay private, repeated preparation is harmless'

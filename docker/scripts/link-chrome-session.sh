#!/usr/bin/env bash
# Profile and LevelDB stay private; saved-password SQLite databases resolve to
# the machine's shared files, including SQLite's journals beside those files.
#
# Cookies stay private to each profile: wk-browser-sync moves login state
# between sessions and must be able to keep one session signed out of a site.
# A shared Cookies file would hand every relaunched browser the other
# sessions' logins straight from disk. An older shared link is replaced by a
# fresh private file, which the launch restore then fills.
set -eu
profile="${1:?usage: link-chrome-session PROFILE}"
shared="${WK_CHROME_SHARED:-/var/lib/weknora/browser/shared}/Default"
mkdir -p "$shared" "$profile/Default"
chmod 0700 "$(dirname "$shared")" "$profile"
exec 9>"$(dirname "$shared")/.link.lock"
flock -w 10 9
for name in Cookies Cookies-journal; do
  if [ -L "$profile/Default/$name" ]; then rm -f "$profile/Default/$name"; fi
done
for name in 'Login Data' 'Login Data For Account'; do
  target="$shared/$name"
  link="$profile/Default/$name"
  if [ -L "$link" ] && [ "$(readlink "$link")" = "$target" ]; then continue; fi
  rm -f "$link"
  ln -s "$target" "$link"
done

#!/usr/bin/env bash
# check-secret-tokens.sh — fail if a real-looking credential was committed to
# the CLI's human-facing docs. An agent-first CLI's docs are where a live
# API key or JWT most easily gets pasted by accident (copying a working
# session into a how-to). Mirrors the safety net lark CLI ships as
# .gitleaks.toml + check-doc-tokens.sh.
#
# Heuristic, low false-positive: a real WeKnora API key is `sk-` followed by a
# long high-entropy body that CONTAINS A DIGIT (e.g.
# sk-bVd4ebLoyn-DKevdkgw527XAakwv4G6Tz6FhgXPlOpBO-Ico); placeholder words like
# `sk-specific` / `sk-staging` have no digit and are short, so they pass. JWTs
# are three base64url segments. Lines that look like placeholders
# (EXAMPLE / REDACTED / <...> / YOUR_ / xxxx) are ignored.
set -euo pipefail
cd "$(dirname "$0")/.."

# Doc surface to scan (where a pasted live token is the real risk).
FILES=$(git ls-files 'skills/**/*.md' 'README.md' 'AGENTS.md' 'CHANGELOG.md' 'ROADMAP.md' 2>/dev/null || true)
[ -z "$FILES" ] && { echo "no doc files to scan"; exit 0; }

# sk- key: prefix + >=24 body chars including at least one digit.
KEY_RE='sk-[A-Za-z0-9_-]*[0-9][A-Za-z0-9_-]*'
# Only flag long bodies; require total match length >= 27 (sk- + 24).
JWT_RE='eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}'
PLACEHOLDER='EXAMPLE|REDACTED|PLACEHOLDER|YOUR_|xxxx|<[a-z]'

fail=0
for f in $FILES; do
  while IFS=: read -r line content; do
    [ -z "$content" ] && continue
    # Skip lines that are clearly illustrative placeholders.
    echo "$content" | grep -qE "$PLACEHOLDER" && continue
    # API key: require >=24 chars after sk- (matched token length >= 27).
    if echo "$content" | grep -oE "$KEY_RE" | grep -qE '^.{27,}$'; then
      echo "POSSIBLE API KEY in $f:$line"
      echo "  $content" | sed -E 's/(sk-[A-Za-z0-9_-]{6})[A-Za-z0-9_-]+/\1…REDACTED/g'
      fail=1
    fi
    if echo "$content" | grep -qE "$JWT_RE"; then
      echo "POSSIBLE JWT in $f:$line"
      fail=1
    fi
  done < <(grep -nE "sk-[A-Za-z0-9_-]|eyJ[A-Za-z0-9_-]" "$f" 2>/dev/null || true)
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "A real-looking credential is committed in docs. Replace it with a"
  echo "placeholder (e.g. sk-EXAMPLE / <api-key> / \$(weknora auth token))."
  exit 1
fi
echo "no committed credentials found in docs"

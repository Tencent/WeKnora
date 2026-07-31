#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

input="$(cat)"
command="$(
  INPUT="$input" node -e 'const j=JSON.parse(process.env.INPUT||"{}"); process.stdout.write(j.command||"")'
)"

if [[ "$command" != *"gh pr create"* ]]; then
  echo '{ "permission": "allow" }'
  exit 0
fi

resolve_base_ref() {
  local ref
  for ref in "${WEKNORA_PR_BASE:-github_public/main}" github_public/main origin/main main; do
    if git rev-parse --verify "$ref" >/dev/null 2>&1; then
      echo "$ref"
      return 0
    fi
  done
  echo "HEAD"
}

base_ref="$(resolve_base_ref)"
needs_frontend=false

if git diff --name-only "${base_ref}...HEAD" 2>/dev/null | grep -q '^frontend/'; then
  needs_frontend=true
fi
if git diff --name-only | grep -q '^frontend/' || git diff --name-only --cached | grep -q '^frontend/'; then
  needs_frontend=true
fi

if [[ "$needs_frontend" != true ]]; then
  echo '{ "permission": "allow" }'
  exit 0
fi

if ! "$ROOT/scripts/verify_frontend_pr.sh"; then
  cat <<'EOF'
{
  "permission": "deny",
  "user_message": "Frontend CI 未通过（npm test / type-check / build）。请先修复后再创建 PR。",
  "agent_message": "Run `scripts/verify_frontend_pr.sh` (or `cd frontend && npm test && npm run type-check && npm run build`) and fix failures before `gh pr create`."
}
EOF
  exit 2
fi

echo '{ "permission": "allow" }'
exit 0

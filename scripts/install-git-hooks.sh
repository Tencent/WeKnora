#!/usr/bin/env bash
# Install WeKnora git hooks (core.hooksPath -> scripts/git-hooks).
#
# Usage (from repo root):
#   ./scripts/install-git-hooks.sh
#
# Bypass hooks for one command:
#   SKIP_HOOKS=1 git commit ...
#   SKIP_HOOKS=1 git push ...
#
# Skip slow go test on pre-push:
#   HOOK_SKIP_TEST=1 git push ...

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOOKS_DIR="$ROOT/scripts/git-hooks"

if [[ ! -d "$HOOKS_DIR" ]]; then
  echo "hooks directory not found: $HOOKS_DIR" >&2
  exit 1
fi

chmod +x "$HOOKS_DIR"/common.sh "$HOOKS_DIR"/pre-commit "$HOOKS_DIR"/pre-push

git -C "$ROOT" config core.hooksPath scripts/git-hooks

echo "Installed git hooks -> scripts/git-hooks"
echo "  pre-commit: whitespace, gofmt (auto-fix), golangci-lint (if installed)"
echo "  pre-push:   mirrors CI (gofmt, golangci-lint, go vet/test, build, frontend/cli)"
echo ""
echo "Optional: SKIP_HOOKS=1 or HOOK_SKIP_TEST=1 — see script header."

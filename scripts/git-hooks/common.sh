#!/usr/bin/env bash
# Shared helpers for WeKnora git hooks (mirrors .github/workflows and PR checklist).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

hook_skip() {
  [[ "${SKIP_HOOKS:-}" == "1" ]]
}

log_step() {
  printf '→ %s\n' "$*"
}

log_ok() {
  printf '✓ %s\n' "$*"
}

log_fail() {
  printf '✗ %s\n' "$*" >&2
}

# File types we expect hand-edited in PRs (skip sample-data / generated blobs).
WHITESPACE_PATHSPECS=(
  '*.go'
  '*.ts' '*.tsx' '*.vue' '*.js' '*.mjs'
  '*.yaml' '*.yml' '*.json'
  '*.sh' '*.sql'
  'Makefile'
  'frontend/package.json'
  'frontend/package-lock.json'
)

# Best-effort fetch so merge-base matches CI (origin/main).
ensure_origin_main() {
  git -C "$ROOT" fetch origin main --quiet 2>/dev/null || true
}

merge_base() {
  ensure_origin_main
  local base
  base="$(git -C "$ROOT" merge-base HEAD origin/main 2>/dev/null || true)"
  if [[ -n "$base" ]]; then
    printf '%s\n' "$base"
    return
  fi
  base="$(git -C "$ROOT" merge-base HEAD main 2>/dev/null || true)"
  if [[ -n "$base" ]]; then
    printf '%s\n' "$base"
    return
  fi
  git -C "$ROOT" rev-parse HEAD~1
}

# Prefer the open PR's base SHA (matches GitHub CI diff) when gh is available.
pr_diff_base_ref() {
  ensure_origin_main
  if command -v gh >/dev/null 2>&1; then
    local base_sha
    base_sha="$(gh pr view --json baseRefOid --jq .baseRefOid 2>/dev/null || true)"
    if [[ -n "$base_sha" && "$base_sha" != "null" ]]; then
      printf '%s\n' "$base_sha"
      return
    fi
  fi
  if git -C "$ROOT" rev-parse --verify origin/main >/dev/null 2>&1; then
    printf '%s\n' "origin/main"
    return
  fi
  printf '%s\n' "main"
}

check_whitespace() {
  log_step "git diff --check (source files)"
  if [[ "${1:-}" == "--cached" ]]; then
    if git -C "$ROOT" diff --cached --quiet -- "${WHITESPACE_PATHSPECS[@]}"; then
      log_ok "no staged source changes to check"
      return 0
    fi
    git -C "$ROOT" diff --check --cached -- "${WHITESPACE_PATHSPECS[@]}"
  else
    if git -C "$ROOT" diff --quiet "$@" -- "${WHITESPACE_PATHSPECS[@]}"; then
      log_ok "no changed source files to check"
      return 0
    fi
    git -C "$ROOT" diff --check "$@" -- "${WHITESPACE_PATHSPECS[@]}"
  fi
  log_ok "no whitespace errors"
}

check_gofmt_files() {
  local mode="$1" # check | write
  shift
  local -a files=("$@")
  if [[ ${#files[@]} -gt 0 ]]; then
    local -a existing=()
    local f rel
    for f in "${files[@]}"; do
      if [[ -f "$f" ]]; then
        existing+=("$f")
        continue
      fi
      rel="${f#"$ROOT"/}"
      if [[ -f "$ROOT/$rel" ]]; then
        existing+=("$ROOT/$rel")
      fi
    done
    files=("${existing[@]}")
  fi
  [[ ${#files[@]} -gt 0 ]] || return 0

  local unformatted
  if [[ "$mode" == "write" ]]; then
    log_step "gofmt (auto-format staged Go files)"
    gofmt -w "${files[@]}"
    # Re-stage formatted files.
    git -C "$ROOT" add -- "${files[@]}"
    log_ok "gofmt applied"
    return 0
  fi

  log_step "gofmt (check)"
  unformatted="$(gofmt -l "${files[@]}")"
  if [[ -n "$unformatted" ]]; then
    log_fail "the following Go files are not gofmt-formatted:"
    printf '%s\n' "$unformatted" >&2
    log_fail "run: gofmt -w <files>  (or commit again after pre-commit auto-format)"
    return 1
  fi
  log_ok "gofmt"
}

collect_go_packages_from_files() {
  local -a files=("$@")
  local f dir pkg
  local -a pkgs=()
  for f in "${files[@]}"; do
    [[ "$f" == *.go ]] || continue
    dir="$(dirname "$f")"
    pkg="$(cd "$ROOT/$dir" && go list . 2>/dev/null)" || continue
    pkgs+=("$pkg")
  done
  if [[ ${#pkgs[@]} -eq 0 ]]; then
    return 0
  fi
  printf '%s\n' "${pkgs[@]}" | sort -u
}

run_golangci_if_available() {
  local from_rev="$1"
  if ! command -v golangci-lint >/dev/null 2>&1; then
    log_step "golangci-lint not installed; skip (install: https://golangci-lint.run)"
    return 0
  fi
  log_step "golangci-lint run --new-from-rev=$from_rev ./..."
  (
    cd "$ROOT"
    golangci-lint run --new-from-rev="$from_rev" ./...
  )
  log_ok "golangci-lint"
}

run_full_app_vet() {
  # Mirrors .github/workflows/app.yml: vet every app package (excl. docreader).
  log_step "go vet (all app packages, like CI)"
  (
    cd "$ROOT"
    local -a pkgs=()
    while IFS= read -r line; do
      [[ -n "$line" ]] && pkgs+=("$line")
    done < <(go list ./... | grep -v '/docreader/' || true)
    go vet "${pkgs[@]}"
  )
  log_ok "go vet"
}

run_go_test_packages() {
  local -a pkgs=("$@")
  if [[ ${#pkgs[@]} -eq 0 ]]; then
    log_step "no changed Go packages; skip go test"
    return 0
  fi
  if [[ "${HOOK_SKIP_TEST:-}" == "1" ]]; then
    log_step "HOOK_SKIP_TEST=1; skip go test"
    return 0
  fi
  log_step "go test (${#pkgs[@]} changed package(s))"
  (
    cd "$ROOT"
    go test -count=1 "${pkgs[@]}"
  )
  log_ok "go test"
}

run_app_vet_test() {
  run_full_app_vet
  run_go_test_packages "$@"
}

run_cli_checks() {
  log_step "cli: go vet && go test"
  (
    cd "$ROOT/cli"
    go vet ./...
    if [[ "${HOOK_SKIP_TEST:-}" == "1" ]]; then
      log_step "HOOK_SKIP_TEST=1; skip cli go test"
    else
      go test -count=1 ./...
    fi
  )
  log_ok "cli checks"
}

run_frontend_checks() {
  log_step "frontend: verify (test, type-check, build)"
  "$ROOT/scripts/verify_frontend_pr.sh"
  log_ok "frontend checks"
}

paths_match_prefix() {
  local prefix="$1"
  shift
  local p
  for p in "$@"; do
    [[ "$p" == "$prefix"* ]] && return 0
  done
  return 1
}

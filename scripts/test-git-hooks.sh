#!/usr/bin/env bash
# Verify that app checks cover unchanged callers and propagate failures.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/scripts/git-hooks" "$fixture/bin"
cp "$repo_root/scripts/git-hooks/common.sh" "$fixture/scripts/git-hooks/"
export HOOK_TEST_LOG="$fixture/go.log"
export LOG_FORMAT="developer-custom-template"
cat > "$fixture/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$HOOK_TEST_LOG"
case "$1" in
  list)
    [[ "${HOOK_TEST_FAIL:-}" != list ]] || exit 1
    printf '%s\n' example/internal/agent/tools example/internal/application/service example/docreader/proto
    ;;
  vet|test)
    if [[ "$1" == test && -n "${LOG_FORMAT:-}" ]]; then
      echo "App tests inherited the developer log template" >&2
      exit 1
    fi
    [[ "${HOOK_TEST_FAIL:-}" != "$1" ]] || exit 1
    ;;
  *) echo "Unexpected go invocation: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "$fixture/bin/go"
export PATH="$fixture/bin:$PATH"
unset HOOK_SKIP_TEST HOOK_TEST_FAIL

run_checks() {
  bash -c 'source "$1"; run_app_vet_test' bash "$fixture/scripts/git-hooks/common.sh"
}

run_checks
cat > "$fixture/expected.log" <<'EOF'
list ./...
vet example/internal/agent/tools example/internal/application/service
test -count=1 example/internal/agent/tools example/internal/application/service
EOF
diff -u "$fixture/expected.log" "$HOOK_TEST_LOG"

# Keep the explicit local opt-out, but it must not skip vet.
: > "$HOOK_TEST_LOG"
HOOK_SKIP_TEST=1 run_checks
head -n 2 "$fixture/expected.log" > "$fixture/skip.log"
diff -u "$fixture/skip.log" "$HOOK_TEST_LOG"

for phase in list vet test; do
  : > "$HOOK_TEST_LOG"
  if HOOK_TEST_FAIL="$phase" run_checks > "$fixture/failure.log" 2>&1; then
    echo "App checks accepted a failing go $phase" >&2
    exit 1
  fi
  case "$phase" in
    list) head -n 1 "$fixture/expected.log" ;;
    vet) head -n 2 "$fixture/expected.log" ;;
    test) cat "$fixture/expected.log" ;;
  esac > "$fixture/failure-expected.log"
  diff -u "$fixture/failure-expected.log" "$HOOK_TEST_LOG"
done

echo 'Git hook coverage checks passed'

#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Load local runtime configuration without printing credential values.
set -a
if [[ -f .env ]]; then
  # shellcheck disable=SC1091
  source .env
fi
if [[ -f .env.local ]]; then
  # shellcheck disable=SC1091
  source .env.local
fi
set +a

# Final Benchmark assumes migrations and model rows were provisioned already.
export AUTO_MIGRATE=false
export GOCACHE="${GOCACHE:-/tmp/weknora-go-cache}"

execution_mode="${BENCHMARK_EXECUTION_MODE:-strict}"
case "$execution_mode" in
  strict)
    default_output_dir="artifacts/rhino_2026_final/benchmark"
    ;;
  custom)
    default_output_dir="artifacts/rhino_2026_final/benchmark_custom"
    ;;
  *)
    echo "BENCHMARK_EXECUTION_MODE must be strict or custom" >&2
    exit 1
    ;;
esac

args=(
  --profile config/benchmark/final_v1.json
  --execution-mode "$execution_mode"
  --dataset benchmark_v1
  --output-dir "${BENCHMARK_OUTPUT_DIR:-$default_output_dir}"
  --backend-url "${BENCHMARK_BACKEND_URL:-http://127.0.0.1:8080}"
)

if [[ "${BENCHMARK_PREFLIGHT_ONLY:-0}" == "1" ]]; then
  args+=(--preflight-only)
fi

exec go run ./cmd/regression-benchmark "${args[@]}" "$@"

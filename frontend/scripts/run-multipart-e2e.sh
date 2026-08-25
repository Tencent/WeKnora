#!/usr/bin/env bash
set -euo pipefail

frontend_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "${frontend_dir}/.." && pwd)"
backend_port="${CUSTOM_E2E_BACKEND_PORT:-18090}"
frontend_port="${CUSTOM_E2E_FRONTEND_PORT:-15173}"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/weknora-multipart-e2e.XXXXXX")"

backend_pid=""
frontend_pid=""

cleanup() {
  local status=$?
  if [[ -n "${frontend_pid}" ]] && kill -0 "${frontend_pid}" 2>/dev/null; then
    kill "${frontend_pid}" 2>/dev/null || true
    wait "${frontend_pid}" 2>/dev/null || true
  fi
  if [[ -n "${backend_pid}" ]] && kill -0 "${backend_pid}" 2>/dev/null; then
    kill "${backend_pid}" 2>/dev/null || true
    wait "${backend_pid}" 2>/dev/null || true
  fi
  if [[ ${status} -ne 0 ]]; then
    echo "Multipart E2E logs: ${tmp_dir}" >&2
  else
    rm -rf "${tmp_dir}"
  fi
  exit "${status}"
}
trap cleanup EXIT INT TERM

wait_for_url() {
  local url="$1"
  local process_name="$2"
  for _ in $(seq 1 120); do
    if curl --fail --silent --show-error "${url}" >/dev/null; then
      return
    fi
    sleep 1
  done
  echo "${process_name} did not become ready: ${url}" >&2
  return 1
}

(
  cd "${repo_dir}"
  CUSTOM_SERVER_HOST=127.0.0.1 \
  CUSTOM_SERVER_PORT="${backend_port}" \
  CUSTOM_DB_DRIVER=sqlite \
  CUSTOM_DB_PATH="${tmp_dir}/custom-backend.db" \
  CUSTOM_STORAGE_BACKEND=local \
  CUSTOM_STORAGE_DIR="${tmp_dir}/storage" \
  MINIO_BUCKET=vidsage-e2e \
  MINIO_PUBLIC_URL="http://127.0.0.1:${backend_port}/api/custom/files" \
  CUSTOM_DISABLE_WORKERS=true \
  GOCACHE="${tmp_dir}/go-cache" \
  go run ./cmd/custom-backend >"${tmp_dir}/custom-backend.log" 2>&1
) &
backend_pid=$!
wait_for_url "http://127.0.0.1:${backend_port}/healthz" "custom-backend"

(
  cd "${frontend_dir}"
  VITE_CUSTOM_BACKEND_TARGET="http://127.0.0.1:${backend_port}" \
  npm run dev -- --host 127.0.0.1 --port "${frontend_port}" --strictPort >"${tmp_dir}/vite.log" 2>&1
) &
frontend_pid=$!
wait_for_url "http://127.0.0.1:${frontend_port}/" "Vite"

cd "${frontend_dir}"
PLAYWRIGHT_BASE_URL="http://127.0.0.1:${frontend_port}" \
npx playwright test e2e/multipart-upload.spec.ts

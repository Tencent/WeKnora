#!/usr/bin/env bash
# 健康与模型连通性自检（在已部署的内网机执行）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

# shellcheck disable=SC1091
set -a
source .env
set +a

FRONTEND="http://127.0.0.1:${FRONTEND_PORT:-18080}"
APP="http://127.0.0.1:${APP_PORT:-18081}"

printf '== compose ==\n'
docker compose ps

printf '\n== frontend ==\n'
curl -sS -o /dev/null -w 'HTTP %{http_code}\n' "${FRONTEND}/" || true

printf '\n== app health ==\n'
curl -sS "${APP}/health" || true
echo

printf '\n== gateway chat ==\n'
curl -sS -m 30 -X POST "${LLM_BASE_URL}/chat/completions" \
  -H "Authorization: Bearer ${LLM_API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"${LLM_MODEL_NAME}\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}],\"stream\":false}" \
  | head -c 800
echo

printf '\n== gateway embedding ==\n'
curl -sS -m 30 -X POST "${EMBEDDING_BASE_URL}/embeddings" \
  -H "Authorization: Bearer ${EMBEDDING_API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"${EMBEDDING_MODEL_NAME}\",\"input\":\"要向量化的文本\"}" \
  | head -c 400
echo

printf '\n== gateway rerank ==\n'
curl -sS -m 30 -X POST "${RERANK_BASE_URL}/rerank" \
  -H "Authorization: Bearer ${RERANK_API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"${RERANK_MODEL_NAME}\",\"query\":\"搜索query\",\"documents\":[\"文档1\",\"文档2\"]}" \
  | head -c 400
echo

printf '\n== schema drift ==\n'
./scripts/check-schema-drift.sh

printf '\n== app builtin model logs ==\n'
docker compose logs app --tail 100 | grep -E 'Built-in model|nmg-rs|SSRF' || true

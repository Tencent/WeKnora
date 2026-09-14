#!/usr/bin/env bash
# 从 GitHub Actions「Pack Offline Images」制品下载镜像到 images/
# 用法: ./scripts/download-from-ci.sh [run_id|v0.8.22]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
IMAGES_DIR="${ROOT_DIR}/images"
REPO="${GITHUB_REPO:-binbin/WeKnora}"
ARG="${1:-}"

mkdir -p "${IMAGES_DIR}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

if [[ -z "${ARG}" ]]; then
  RUN_ID="$(gh run list --repo "${REPO}" --workflow='Pack Offline Images' \
    --limit 1 --json databaseId,conclusion \
    --jq 'map(select(.conclusion=="success"))[0].databaseId')"
elif [[ "${ARG}" =~ ^[0-9]+$ ]]; then
  RUN_ID="${ARG}"
else
  TAG="${ARG}"
  RUN_ID="$(gh run list --repo "${REPO}" --workflow='Pack Offline Images' \
    --limit 20 --json databaseId,conclusion,displayTitle \
    --jq --arg tag "${TAG}" \
    'map(select(.conclusion=="success" and (.displayTitle|contains($tag))))[0].databaseId')"
fi

[[ -n "${RUN_ID}" && "${RUN_ID}" != "null" ]] || {
  echo "未找到成功的 Pack Offline Images 运行" >&2
  exit 1
}

echo "[download] run_id=${RUN_ID}"
cd "${TMP_DIR}"
gh run download "${RUN_ID}" --repo "${REPO}" -n "treerag-offline-images-v0.8.22" \
  2>/dev/null \
  || gh run download "${RUN_ID}" --repo "${REPO}"

find . -name '*.tar' -exec cp -f {} "${IMAGES_DIR}/" \;
find . -name 'manifest*.txt' -exec cp -f {} "${IMAGES_DIR}/" \;
find . -name 'treerag-images-*.tar.gz' -exec cp -f {} "${IMAGES_DIR}/" \;
ls -lh "${IMAGES_DIR}"
echo "[download] 完成 → ${IMAGES_DIR}"

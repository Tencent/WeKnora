#!/usr/bin/env bash
# 修复 schema_migrations 超前、真实表结构落后的漂移。
# 默认回拨到 77（含 078 custom_metadata；78 会跳过该迁移），再重启 app 让 AUTO_MIGRATE 重跑。
#
# 用法：./scripts/repair-schema-drift.sh
# 可选：REPAIR_TO=77 ./scripts/repair-schema-drift.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

# shellcheck disable=SC1091
set -a
source .env
set +a

DB_USER="${DB_USER:-postgres}"
DB_NAME="${DB_NAME:-TreeRAG}"
REPAIR_TO="${REPAIR_TO:-77}"

if [[ -z "${DB_PASSWORD:-}" ]]; then
  printf 'ERROR: DB_PASSWORD empty; source .env first\n' >&2
  exit 1
fi

printf 'Current migration state:\n'
sudo docker compose exec -T -e PGPASSWORD="${DB_PASSWORD}" postgres \
  psql -U "${DB_USER}" -d "${DB_NAME}" -c \
  "SELECT version, dirty FROM schema_migrations;"

printf '\nForce schema_migrations to version=%s dirty=false ...\n' "${REPAIR_TO}"
sudo docker compose exec -T -e PGPASSWORD="${DB_PASSWORD}" postgres \
  psql -v ON_ERROR_STOP=1 -U "${DB_USER}" -d "${DB_NAME}" -c \
  "UPDATE schema_migrations SET version = ${REPAIR_TO}, dirty = false;"

printf 'Restarting app so AUTO_MIGRATE re-applies pending migrations...\n'
sudo docker compose restart app

printf 'Waiting for app healthy...\n'
for _ in $(seq 1 60); do
  if sudo docker compose ps app 2>/dev/null | grep -qi healthy; then
    break
  fi
  sleep 2
done

printf '\nApp migration log:\n'
sudo docker compose logs app --since 3m 2>&1 \
  | grep -iE 'migration|schema drift|Database is up to date|migrated from' \
  | tail -40 || true

printf '\nRe-check drift:\n'
"${SCRIPT_DIR}/check-schema-drift.sh"

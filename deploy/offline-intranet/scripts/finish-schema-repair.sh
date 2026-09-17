#!/usr/bin/env bash
# 完成「回拨重跑」半途 dirty 卡住后的收尾（当前典型：version=93 dirty，缺 078 列）。
# 在 offline-intranet 根目录执行：./scripts/finish-schema-repair.sh
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

if [[ -z "${DB_PASSWORD:-}" ]]; then
  printf 'ERROR: DB_PASSWORD empty\n' >&2
  exit 1
fi

psql_exec() {
  sudo docker compose exec -T -e PGPASSWORD="${DB_PASSWORD}" postgres \
    psql -v ON_ERROR_STOP=1 -U "${DB_USER}" -d "${DB_NAME}" "$@"
}

printf '== before ==\n'
psql_exec -c "SELECT version, dirty FROM schema_migrations;"

printf '\n== apply migration 078 columns (skipped when force-to-78) ==\n'
psql_exec <<'SQL'
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS source_content TEXT NOT NULL DEFAULT '';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS content_revision INT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS index_status VARCHAR(16) NOT NULL DEFAULT 'ready';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS last_editor_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS context_header TEXT NOT NULL DEFAULT '';
UPDATE chunks SET source_content = content WHERE source_content = '';
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS custom_metadata JSONB NOT NULL DEFAULT '{}'::JSONB;
SQL

# If 093 tables already exist, mark 93 applied and clear dirty so 94+ can run.
printf '\n== clear dirty at 93 (browser_* already present) ==\n'
psql_exec -c "UPDATE schema_migrations SET version = 93, dirty = false;"

printf '\n== restart app (AUTO_MIGRATE 94→108) ==\n'
sudo docker compose restart app

for _ in $(seq 1 60); do
  if sudo docker compose ps app 2>/dev/null | grep -qi healthy; then
    break
  fi
  sleep 2
done

printf '\n== migration log ==\n'
sudo docker compose logs app --since 3m 2>&1 \
  | grep -iE 'migration|dirty|up to date|migrated from|schema drift|failed' \
  | tail -50 || true

printf '\n== after ==\n'
psql_exec -c "SELECT version, dirty FROM schema_migrations;"

if [[ -x "${SCRIPT_DIR}/check-schema-drift.sh" ]]; then
  "${SCRIPT_DIR}/check-schema-drift.sh"
fi

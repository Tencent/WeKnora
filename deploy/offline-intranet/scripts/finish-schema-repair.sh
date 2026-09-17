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

printf '\n== apply migration 061/075 wiki columns (skipped when force-to-78+) ==\n'
psql_exec <<'SQL'
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS parent_slug VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS category_path JSONB DEFAULT '[]'::JSONB;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS wiki_path VARCHAR(1024) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS depth INT NOT NULL DEFAULT 0;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 0;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS last_edit_source VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS last_editor_id VARCHAR(64) NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS wiki_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    sort_order        INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);
CREATE TABLE IF NOT EXISTS wiki_page_revisions (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id         VARCHAR(36) NOT NULL,
    slug            VARCHAR(255) NOT NULL,
    version         INT NOT NULL,
    title           VARCHAR(512) NOT NULL DEFAULT '',
    page_type       VARCHAR(32) NOT NULL DEFAULT 'summary',
    status          VARCHAR(32) NOT NULL DEFAULT 'published',
    content         TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    aliases         JSONB DEFAULT '[]'::JSONB,
    edit_source     VARCHAR(16) NOT NULL DEFAULT '',
    editor_id       VARCHAR(64) NOT NULL DEFAULT '',
    edited_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
SQL

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

# Advance past non-idempotent image migrations that already partially applied.
# v0.8.24 image still has bare CREATE/ADD in 093/094; objects already exist.
printf '\n== clear dirty / advance past 094 ==\n'
psql_exec -c "UPDATE schema_migrations SET version = 94, dirty = false;"

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

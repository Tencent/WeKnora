#!/usr/bin/env bash
# 检测 schema_migrations 与真实表结构是否漂移（version 已高但列/表缺失）。
# 用法：在 offline-intranet 根目录执行 ./scripts/check-schema-drift.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

# shellcheck disable=SC1091
set -a
# bash/source；若用 dash 请改: . ./.env
source .env
set +a

DB_USER="${DB_USER:-postgres}"
DB_NAME="${DB_NAME:-TreeRAG}"

if [[ -z "${DB_PASSWORD:-}" ]]; then
  printf 'ERROR: DB_PASSWORD empty; source .env first\n' >&2
  exit 1
fi

psql_exec() {
  sudo docker compose exec -T -e PGPASSWORD="${DB_PASSWORD}" postgres \
    psql -v ON_ERROR_STOP=1 -U "${DB_USER}" -d "${DB_NAME}" "$@"
}

printf '== schema_migrations ==\n'
psql_exec -c "SELECT version, dirty FROM schema_migrations;"

printf '\n== critical objects (empty result => missing) ==\n'
psql_exec -c "
SELECT kind, name FROM (
  SELECT 'table' AS kind, 'tenant_sandbox_configs' AS name
    WHERE to_regclass('public.tenant_sandbox_configs') IS NULL
  UNION ALL SELECT 'table', 'org_units'
    WHERE to_regclass('public.org_units') IS NULL
  UNION ALL SELECT 'table', 'guest_link_channels'
    WHERE to_regclass('public.guest_link_channels') IS NULL
  UNION ALL SELECT 'table', 'agent_publish_api_keys'
    WHERE to_regclass('public.agent_publish_api_keys') IS NULL
  UNION ALL SELECT 'column', 'knowledges.custom_metadata'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='knowledges' AND column_name='custom_metadata')
  UNION ALL SELECT 'column', 'chunks.source_content'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='chunks' AND column_name='source_content')
  UNION ALL SELECT 'column', 'knowledges.folder_path'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='knowledges' AND column_name='folder_path')
  UNION ALL SELECT 'column', 'messages.artifacts'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='messages' AND column_name='artifacts')
  UNION ALL SELECT 'column', 'messages.usage'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='messages' AND column_name='usage')
  UNION ALL SELECT 'column', 'sessions.sandbox_config_id'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='sessions' AND column_name='sandbox_config_id')
  UNION ALL SELECT 'column', 'knowledge_bases.org_unit_id'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='knowledge_bases' AND column_name='org_unit_id')
  UNION ALL SELECT 'column', 'knowledge_bases.share_with_descendants'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='knowledge_bases' AND column_name='share_with_descendants')
  UNION ALL SELECT 'column', 'guest_link_channels.session_secret'
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
      WHERE table_schema='public' AND table_name='guest_link_channels' AND column_name='session_secret')
) t ORDER BY 1, 2;
"

missing_count="$(psql_exec -At -c "
SELECT COUNT(*) FROM (
  SELECT 1 WHERE to_regclass('public.tenant_sandbox_configs') IS NULL
  UNION ALL SELECT 1 WHERE to_regclass('public.org_units') IS NULL
  UNION ALL SELECT 1 WHERE to_regclass('public.guest_link_channels') IS NULL
  UNION ALL SELECT 1 WHERE to_regclass('public.agent_publish_api_keys') IS NULL
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='knowledges' AND column_name='custom_metadata')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='chunks' AND column_name='source_content')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='knowledges' AND column_name='folder_path')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='messages' AND column_name='artifacts')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='messages' AND column_name='usage')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='sessions' AND column_name='sandbox_config_id')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='knowledge_bases' AND column_name='org_unit_id')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='knowledge_bases' AND column_name='share_with_descendants')
  UNION ALL SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='guest_link_channels' AND column_name='session_secret')
) t;
")"

if [[ "${missing_count}" != "0" ]]; then
  printf '\nFAIL: schema drift — %s critical object(s) missing.\n' "${missing_count}" >&2
  printf 'Fix: ./scripts/finish-schema-repair.sh  (dirty@93) or ./scripts/repair-schema-drift.sh\n' >&2
  exit 1
fi

printf '\nOK: critical schema objects present.\n'

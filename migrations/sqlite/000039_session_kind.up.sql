ALTER TABLE sessions ADD COLUMN kind VARCHAR(32) NOT NULL DEFAULT '';

UPDATE sessions SET kind = 'assistant'
WHERE CASE WHEN json_valid(agent_config) THEN json_extract(agent_config, '$.agent_id') END = 'builtin-weknora-assistant';

CREATE INDEX IF NOT EXISTS idx_sessions_tenant_kind_user_updated
    ON sessions (tenant_id, kind, user_id, updated_at DESC)
    WHERE deleted_at IS NULL;

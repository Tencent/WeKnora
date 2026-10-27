-- Remembered agent approvals: "allow for the rest of this conversation" and
-- the grants an automation carries between runs.
CREATE TABLE IF NOT EXISTS agent_grants (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    scope VARCHAR(16) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    grant_key VARCHAR(512) NOT NULL,
    label TEXT,
    binding_hash VARCHAR(64) NOT NULL DEFAULT '',
    risk VARCHAR(16) NOT NULL DEFAULT 'write',
    prompt_id VARCHAR(36),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_grants_key
    ON agent_grants(tenant_id, user_id, scope, scope_id, grant_key);
CREATE INDEX IF NOT EXISTS idx_agent_grants_scope ON agent_grants(tenant_id, scope, scope_id);

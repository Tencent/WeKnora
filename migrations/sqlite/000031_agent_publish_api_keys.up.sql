-- Migration: 000015_agent_publish_api_keys
-- (align with versioned 000087)

CREATE TABLE IF NOT EXISTS agent_publish_api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    key_prefix VARCHAR(32) NOT NULL,
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    api_key TEXT NOT NULL DEFAULT '',
    last_used_at DATETIME,
    expires_at DATETIME,
    revoked_at DATETIME,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_publish_api_keys_tenant_agent
    ON agent_publish_api_keys(tenant_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_publish_api_keys_revoked_at
    ON agent_publish_api_keys(revoked_at);

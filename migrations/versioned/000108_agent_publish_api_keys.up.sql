-- Migration: 000087_agent_publish_api_keys
DO $$ BEGIN RAISE NOTICE '[Migration 000087] Creating agent_publish_api_keys'; END $$;

CREATE TABLE IF NOT EXISTS agent_publish_api_keys (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    key_prefix VARCHAR(32) NOT NULL,
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    api_key TEXT NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_publish_api_keys_tenant_agent
    ON agent_publish_api_keys(tenant_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_publish_api_keys_revoked_at
    ON agent_publish_api_keys(revoked_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000087] agent_publish_api_keys ready'; END $$;

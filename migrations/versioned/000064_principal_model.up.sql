-- Migration: 000064_principal_model
-- Description: Principal-based identity for MCP OAuth, tenant API config, and session ownership.

DO $$ BEGIN RAISE NOTICE '[Migration 000064] Adding principal columns to MCP OAuth tokens...'; END $$;

ALTER TABLE mcp_oauth_tokens
    ADD COLUMN IF NOT EXISTS principal_type VARCHAR(32),
    ADD COLUMN IF NOT EXISTS principal_id VARCHAR(512);

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN user_id TYPE VARCHAR(512);

UPDATE mcp_oauth_tokens
SET principal_type = 'web_user',
    principal_id = user_id
WHERE (principal_type IS NULL OR principal_type = '')
  AND user_id IS NOT NULL
  AND user_id <> '';

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN principal_type SET NOT NULL,
    ALTER COLUMN principal_id SET NOT NULL;

DROP INDEX IF EXISTS idx_mcp_oauth_tokens_tenant_user_svc;

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_principal_svc
    ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_principal
    ON mcp_oauth_tokens(principal_type, principal_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000064] MCP OAuth principal columns ready'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000064] Adding tenant API principal config...'; END $$;

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS api_principal_config JSONB;

DO $$ BEGIN RAISE NOTICE '[Migration 000064] tenant API principal config ready'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000064] Widening sessions.user_id to VARCHAR(512)'; END $$;

ALTER TABLE sessions
    ALTER COLUMN user_id TYPE VARCHAR(512);

DO $$ BEGIN RAISE NOTICE '[Migration 000064] principal model migration complete'; END $$;

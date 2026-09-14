-- Mirrors versioned migration 000064_principal_model:
-- principal identity for MCP OAuth tokens and tenant API config.

ALTER TABLE tenants ADD COLUMN api_principal_config TEXT;

ALTER TABLE mcp_oauth_tokens ADD COLUMN principal_type VARCHAR(32);
ALTER TABLE mcp_oauth_tokens ADD COLUMN principal_id VARCHAR(512);

UPDATE mcp_oauth_tokens
SET principal_type = 'web_user',
    principal_id = user_id
WHERE (principal_type IS NULL OR principal_type = '')
  AND user_id IS NOT NULL
  AND user_id <> '';

DROP INDEX IF EXISTS idx_mcp_oauth_tokens_tenant_user_svc;

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_principal_svc
    ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_principal
    ON mcp_oauth_tokens(principal_type, principal_id);

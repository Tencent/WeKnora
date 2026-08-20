DROP INDEX IF EXISTS idx_mcp_oauth_tokens_principal;
DROP INDEX IF EXISTS idx_mcp_oauth_tokens_tenant_principal_svc;

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_user_svc
    ON mcp_oauth_tokens(tenant_id, user_id, service_id);

ALTER TABLE mcp_oauth_tokens DROP COLUMN principal_id;
ALTER TABLE mcp_oauth_tokens DROP COLUMN principal_type;
ALTER TABLE tenants DROP COLUMN api_principal_config;

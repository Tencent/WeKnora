DROP INDEX IF EXISTS idx_mcp_oauth_tokens_principal;
DROP INDEX IF EXISTS idx_mcp_oauth_tokens_tenant_principal_svc;

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN user_id TYPE VARCHAR(64) USING LEFT(user_id, 64);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_user_svc
    ON mcp_oauth_tokens(tenant_id, user_id, service_id);

ALTER TABLE mcp_oauth_tokens
    DROP COLUMN IF EXISTS principal_id,
    DROP COLUMN IF EXISTS principal_type;

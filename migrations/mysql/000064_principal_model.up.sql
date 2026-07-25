-- MySQL 8 translation of 000064_principal_model.
ALTER TABLE mcp_oauth_tokens
    ADD COLUMN principal_type VARCHAR(32),
    ADD COLUMN principal_id VARCHAR(512),
    MODIFY COLUMN user_id VARCHAR(512) NOT NULL;

UPDATE mcp_oauth_tokens
SET principal_type = 'web_user', principal_id = user_id
WHERE (principal_type IS NULL OR principal_type = '')
  AND user_id IS NOT NULL AND user_id <> '';

ALTER TABLE mcp_oauth_tokens
    MODIFY COLUMN principal_type VARCHAR(32) NOT NULL,
    MODIFY COLUMN principal_id VARCHAR(512) NOT NULL;

DROP INDEX idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens;
CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_principal_svc
    ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);
CREATE INDEX idx_mcp_oauth_tokens_principal
    ON mcp_oauth_tokens(principal_type, principal_id);

ALTER TABLE tenants ADD COLUMN api_principal_config JSON;
ALTER TABLE sessions MODIFY COLUMN user_id VARCHAR(512);

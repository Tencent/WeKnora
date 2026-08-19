ALTER TABLE mcp_oauth_tokens DROP COLUMN principal_id;
ALTER TABLE mcp_oauth_tokens DROP COLUMN principal_type;
ALTER TABLE tenants DROP COLUMN api_principal_config;

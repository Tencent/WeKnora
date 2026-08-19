-- Mirrors versioned migration 000064_principal_model:
-- principal identity for MCP OAuth tokens and tenant API config.

ALTER TABLE tenants ADD COLUMN api_principal_config TEXT;

ALTER TABLE mcp_oauth_tokens ADD COLUMN principal_type VARCHAR(32);
ALTER TABLE mcp_oauth_tokens ADD COLUMN principal_id VARCHAR(512);

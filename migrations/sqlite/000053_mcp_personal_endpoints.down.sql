DROP TABLE IF EXISTS mcp_endpoint_settings;
DROP INDEX IF EXISTS idx_mcp_endpoints_owner;
DELETE FROM mcp_endpoints WHERE kind = 'personal';
ALTER TABLE mcp_endpoints DROP COLUMN user_id;
ALTER TABLE mcp_endpoints DROP COLUMN kind;

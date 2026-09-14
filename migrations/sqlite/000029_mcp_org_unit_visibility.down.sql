-- SQLite rollback: mirror 000085_mcp_org_unit_visibility

DROP TABLE IF EXISTS mcp_shares;
DROP INDEX IF EXISTS idx_mcp_services_tenant_org_unit;

-- SQLite cannot DROP COLUMN reliably across versions; leave columns if present.

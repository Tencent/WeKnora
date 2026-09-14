-- SQLite cannot DROP COLUMN safely across versions; leave column unused on down.
DROP INDEX IF EXISTS idx_custom_agents_tenant_org_unit;

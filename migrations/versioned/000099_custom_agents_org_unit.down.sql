DROP INDEX IF EXISTS idx_custom_agents_tenant_org_unit;
ALTER TABLE custom_agents DROP COLUMN IF EXISTS org_unit_id;

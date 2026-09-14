-- SQLite: mirror 000078_custom_agents_org_unit

ALTER TABLE custom_agents ADD COLUMN org_unit_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_custom_agents_tenant_org_unit
    ON custom_agents (tenant_id, org_unit_id);

-- Backfill from primary org_unit membership.
UPDATE custom_agents
SET org_unit_id = (
    SELECT oum.org_unit_id
    FROM org_unit_members oum
    WHERE oum.tenant_id = custom_agents.tenant_id
      AND oum.user_id = custom_agents.created_by
      AND oum.is_primary = 1
    LIMIT 1
)
WHERE created_by <> '' AND (org_unit_id IS NULL OR org_unit_id = '');

UPDATE custom_agents
SET org_unit_id = (
    SELECT oum.org_unit_id
    FROM org_unit_members oum
    WHERE oum.tenant_id = custom_agents.tenant_id
      AND oum.user_id = custom_agents.created_by
    ORDER BY oum.is_primary DESC, oum.created_at ASC
    LIMIT 1
)
WHERE created_by <> '' AND (org_unit_id IS NULL OR org_unit_id = '');

-- Migration: 000078_custom_agents_org_unit
-- Description: Stamp custom_agents with the creator's org unit at create time
--              so chat visibility is stable when users change departments.

DO $$ BEGIN RAISE NOTICE '[Migration 000078] Adding org_unit_id to custom_agents...'; END $$;

ALTER TABLE custom_agents
    ADD COLUMN IF NOT EXISTS org_unit_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_custom_agents_tenant_org_unit
    ON custom_agents (tenant_id, org_unit_id)
    WHERE deleted_at IS NULL AND is_builtin = FALSE;

-- Backfill from creator's primary org_unit membership.
UPDATE custom_agents ca
SET org_unit_id = oum.org_unit_id
FROM org_unit_members oum
WHERE ca.created_by <> ''
  AND ca.org_unit_id = ''
  AND oum.tenant_id = ca.tenant_id
  AND oum.user_id = ca.created_by
  AND oum.is_primary = TRUE;

-- Fallback: any membership if no primary was set.
UPDATE custom_agents ca
SET org_unit_id = sub.org_unit_id
FROM (
    SELECT DISTINCT ON (tenant_id, user_id)
        tenant_id, user_id, org_unit_id
    FROM org_unit_members
    ORDER BY tenant_id, user_id, is_primary DESC, created_at ASC
) AS sub
WHERE ca.created_by <> ''
  AND ca.org_unit_id = ''
  AND sub.tenant_id = ca.tenant_id
  AND sub.user_id = ca.created_by;

COMMENT ON COLUMN custom_agents.org_unit_id IS
    'Org unit stamped at create time; chat lists agents by this exact unit (no descendants)';

DO $$ BEGIN RAISE NOTICE '[Migration 000078] custom_agents.org_unit_id ready'; END $$;

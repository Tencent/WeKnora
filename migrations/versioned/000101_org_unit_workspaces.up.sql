-- Migration: 000080_org_unit_workspaces
-- Description: Map platform root OrgUnits (tenant_id=0) to business
--              workspace Tenants. Enables lazy workspace provisioning
--              named「{组织名}的空间」on first login of org members.

DO $$ BEGIN RAISE NOTICE '[Migration 000080] Creating org_unit_workspaces...'; END $$;

-- Platform catalog OrgUnits use tenant_id = 0 (no business workspace).
-- Existing rows keep their tenant_id; new roots created by system admins
-- are written with tenant_id = 0 and bound here when a workspace is needed.
CREATE TABLE IF NOT EXISTS org_unit_workspaces (
    root_org_unit_id VARCHAR(36) PRIMARY KEY,
    tenant_id        BIGINT NOT NULL UNIQUE,
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_unit_workspaces_tenant
    ON org_unit_workspaces (tenant_id);

COMMENT ON TABLE org_unit_workspaces IS
    'Binds a platform root OrgUnit (tenant_id=0) to its business Tenant workspace';
COMMENT ON COLUMN org_unit_workspaces.root_org_unit_id IS
    'OrgUnit.id of a root node (parent_id empty) in the platform catalog';
COMMENT ON COLUMN org_unit_workspaces.tenant_id IS
    'Business workspace Tenant that members of this org tree share';

DO $$ BEGIN RAISE NOTICE '[Migration 000080] org_unit_workspaces ready'; END $$;

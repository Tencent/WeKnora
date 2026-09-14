DROP INDEX IF EXISTS idx_org_unit_members_tenant_user_unique;

CREATE INDEX IF NOT EXISTS idx_org_unit_members_tenant_user
    ON org_unit_members (tenant_id, user_id);

COMMENT ON TABLE org_unit_members IS
    'Maps users to OrgUnits within a tenant; is_primary marks default unit';

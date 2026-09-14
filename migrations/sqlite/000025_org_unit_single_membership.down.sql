DROP INDEX IF EXISTS idx_org_unit_members_tenant_user_unique;

CREATE INDEX IF NOT EXISTS idx_org_unit_members_tenant_user
    ON org_unit_members (tenant_id, user_id);

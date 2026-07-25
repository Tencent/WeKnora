-- MySQL 8 translation of 000046_org_owner_tenant_id.
ALTER TABLE organizations ADD COLUMN owner_tenant_id BIGINT NOT NULL DEFAULT 0;
CREATE INDEX idx_organizations_owner_tenant ON organizations(owner_tenant_id);

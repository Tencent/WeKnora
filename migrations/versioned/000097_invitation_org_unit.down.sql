-- Rollback: 000076_invitation_org_unit

DROP INDEX IF EXISTS idx_tenant_invitations_org_unit;
ALTER TABLE tenant_invitations DROP COLUMN IF EXISTS org_unit_id;

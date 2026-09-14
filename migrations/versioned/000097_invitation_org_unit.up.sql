-- Migration: 000076_invitation_org_unit
-- Description: Bind tenant invitations to an OrgUnit so accept/register
--              places the invitee into the correct hierarchy node.

DO $$ BEGIN RAISE NOTICE '[Migration 000076] Adding org_unit_id to tenant_invitations...'; END $$;

ALTER TABLE tenant_invitations
    ADD COLUMN IF NOT EXISTS org_unit_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tenant_invitations_org_unit
    ON tenant_invitations (tenant_id, org_unit_id)
    WHERE deleted_at IS NULL;

COMMENT ON COLUMN tenant_invitations.org_unit_id IS
    'OrgUnit the invitee joins on accept; empty when tenant has no hierarchy';

DO $$ BEGIN RAISE NOTICE '[Migration 000076] tenant_invitations.org_unit_id ready'; END $$;

-- Reverse of 000048_tenant_invitations.
DO $$ BEGIN RAISE NOTICE '[Migration 000048] Dropping table: tenant_invitations'; END $$;

DROP INDEX IF EXISTS idx_tenant_invitations_invitee;
DROP INDEX IF EXISTS idx_tenant_invitations_tenant;
DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;
DROP TABLE IF EXISTS tenant_invitations;

DO $$ BEGIN RAISE NOTICE '[Migration 000048] tenant_invitations table dropped'; END $$;

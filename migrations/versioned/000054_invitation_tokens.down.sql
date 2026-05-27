-- Reverse of 000054_invitation_share_links.
DO $$ BEGIN RAISE NOTICE '[Migration 000054] Reverting share-link columns on tenant_invitations'; END $$;

DROP INDEX IF EXISTS idx_tenant_invitations_token;
DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending' AND deleted_at IS NULL;

ALTER TABLE tenant_invitations
    DROP COLUMN IF EXISTS accepted_count,
    DROP COLUMN IF EXISTS token,
    ALTER COLUMN invitee_user_id DROP DEFAULT;

DO $$ BEGIN RAISE NOTICE '[Migration 000054] tenant_invitations reverted'; END $$;

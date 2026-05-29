-- Reverse of 000054_invitation_share_links.
DO $$ BEGIN RAISE NOTICE '[Migration 000054] Reverting share-link columns on tenant_invitations'; END $$;

DROP INDEX IF EXISTS idx_tenant_invitations_token;
DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;

-- Share-link rows have invitee_user_id='' and the up migration's index
-- explicitly excludes those values; the legacy index does not. If we
-- recreate the legacy unique index while multiple share-link rows
-- coexist on a tenant, the CREATE will abort with duplicate-key. Drop
-- the share-link rows before rebuilding so the rollback is idempotent
-- regardless of how many share links the tenant has issued. This is
-- intentionally destructive — share-link rows have no per-user state
-- worth preserving (no specific invitee, no pending acceptance), and
-- the only alternative would be leaving orphaned rows the legacy
-- schema can't represent.
DELETE FROM tenant_invitations
    WHERE invitee_user_id = ''
      AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending' AND deleted_at IS NULL;

ALTER TABLE tenant_invitations
    DROP COLUMN IF EXISTS accepted_count,
    DROP COLUMN IF EXISTS token,
    ALTER COLUMN invitee_user_id DROP DEFAULT;

DO $$ BEGIN RAISE NOTICE '[Migration 000054] tenant_invitations reverted'; END $$;

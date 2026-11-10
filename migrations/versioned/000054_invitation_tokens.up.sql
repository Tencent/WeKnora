-- Migration: 000054_invitation_share_links
-- Extends tenant_invitations with the columns needed for "share link"
-- invitations: a multi-use registration link an Owner can drop in a
-- group chat. The existing per-user invitation flow (rows with a real
-- invitee_user_id) keeps working unchanged.
--
-- Schema changes:
--   * invitee_user_id is given a default empty string. Share-link rows
--     start with no specific invitee — whoever holds the link
--     registers themselves with their own email. Per-user invitations
--     still write the resolved user id at create-time.
--   * token holds the plaintext registration token for share-link
--     rows. We store plaintext (not a hash) deliberately: the threat
--     model is bounded by short TTL, revocability, and the fact that
--     consuming the link only grants membership in one tenant.
--     Plaintext lets the management UI re-display the link on demand
--     so the Owner doesn't have to "copy now or revoke and re-issue".
--   * accepted_count counts how many users have completed registration
--     through the row. Per-user invitations cap at 1 and flip to
--     accepted in the same step; share-link rows accumulate — surfaced
--     in the management UI as "N 人已加入" so Owners can tell whether
--     a link is fresh or has already been used widely.
--
-- Index changes:
--   * The pending-uniqueness index on (tenant_id, invitee_user_id) is
--     relaxed to skip empty values so multiple share-link rows can
--     coexist on the same tenant.
--   * token gets its own partial unique index for lookup at
--     /auth/register-by-invite.
DO $$ BEGIN RAISE NOTICE '[Migration 000054] Extending tenant_invitations for share-link invitations'; END $$;

ALTER TABLE tenant_invitations
    ALTER COLUMN invitee_user_id SET DEFAULT '',
    ADD COLUMN IF NOT EXISTS token VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS accepted_count INTEGER NOT NULL DEFAULT 0;

DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending'
      AND deleted_at IS NULL
      AND invitee_user_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_token
    ON tenant_invitations(token)
    WHERE token <> '' AND deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000054] tenant_invitations extended'; END $$;

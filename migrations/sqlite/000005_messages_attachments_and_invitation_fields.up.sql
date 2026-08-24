-- Mirrors versioned migrations:
--   000034_add_attachments        messages.attachments
--   000054_invitation_tokens      tenant_invitations.token / accepted_count

ALTER TABLE messages ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]';

ALTER TABLE tenant_invitations ADD COLUMN token VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE tenant_invitations ADD COLUMN accepted_count INTEGER NOT NULL DEFAULT 0;

-- Share-link rows use invitee_user_id=''; relax the pending uniqueness index
-- so multiple share links can coexist on one tenant (see versioned 000054).
DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending'
      AND deleted_at IS NULL
      AND invitee_user_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_token
    ON tenant_invitations(token)
    WHERE token <> '' AND deleted_at IS NULL;

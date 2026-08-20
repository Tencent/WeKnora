DROP INDEX IF EXISTS idx_tenant_invitations_token;
DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;

-- Legacy unique index cannot coexist with multiple share-link rows.
DELETE FROM tenant_invitations
    WHERE invitee_user_id = ''
      AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending' AND deleted_at IS NULL;

ALTER TABLE tenant_invitations DROP COLUMN accepted_count;
ALTER TABLE tenant_invitations DROP COLUMN token;
ALTER TABLE messages DROP COLUMN attachments;

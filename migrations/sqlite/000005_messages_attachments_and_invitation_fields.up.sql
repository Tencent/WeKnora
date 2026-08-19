-- Mirrors versioned migrations:
--   000034_add_attachments        messages.attachments
--   000054_invitation_tokens      tenant_invitations.token / accepted_count

ALTER TABLE messages ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]';

ALTER TABLE tenant_invitations ADD COLUMN token VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE tenant_invitations ADD COLUMN accepted_count INTEGER NOT NULL DEFAULT 0;

-- MySQL 8 translation of 000054_invitation_tokens.
-- MySQL has no partial unique indexes; pending-invitation and active-token
-- uniqueness remain service-enforced, as in the base invitation migration.

ALTER TABLE tenant_invitations
    MODIFY COLUMN invitee_user_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN token VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN accepted_count INT NOT NULL DEFAULT 0;

CREATE INDEX idx_tenant_invitations_token ON tenant_invitations(token);

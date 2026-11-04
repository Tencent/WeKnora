-- Per-user saved logins for the sandbox browser's secure credential form.
-- The password is only in secret_ciphertext (AES-256-GCM, SYSTEM_AES_KEY).
CREATE TABLE IF NOT EXISTS credential_vault_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'login',
    title VARCHAR(200) NOT NULL,
    site VARCHAR(255) NOT NULL,
    domains JSONB NOT NULL DEFAULT '[]',
    username VARCHAR(256) NOT NULL,
    secret_ciphertext TEXT NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_credential_vault_items_owner_site_user
    ON credential_vault_items(tenant_id, user_id, site, username) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_credential_vault_items_deleted_at ON credential_vault_items(deleted_at);

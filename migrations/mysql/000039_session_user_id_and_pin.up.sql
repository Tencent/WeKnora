-- MySQL 8 translation of 000039_session_user_id_and_pin.
ALTER TABLE sessions
    ADD COLUMN user_id VARCHAR(36),
    ADD COLUMN is_pinned TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN pinned_at TIMESTAMP NULL;
CREATE INDEX idx_sessions_tenant_user_pin
    ON sessions(tenant_id, user_id, is_pinned DESC, pinned_at DESC, updated_at DESC);

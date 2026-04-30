-- Rollback: 000039_session_user_id_and_pin

DROP INDEX IF EXISTS idx_sessions_tenant_user_pin;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS pinned_at,
    DROP COLUMN IF EXISTS is_pinned,
    DROP COLUMN IF EXISTS user_id;

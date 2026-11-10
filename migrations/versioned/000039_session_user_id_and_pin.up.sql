-- Migration: 000039_session_user_id_and_pin
-- Description: Add user_id, is_pinned, pinned_at to sessions for per-user
--              session ownership and user-level pinning. Existing rows keep
--              user_id = NULL and stay visible at the tenant level for
--              backward compatibility.

DO $$ BEGIN RAISE NOTICE '[Migration 000039] Adding user_id/is_pinned/pinned_at to sessions'; END $$;

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS user_id VARCHAR(36),
    ADD COLUMN IF NOT EXISTS is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMP WITH TIME ZONE;

-- Index for the common list query:
--   WHERE tenant_id = ? AND (user_id = ? OR user_id IS NULL) AND deleted_at IS NULL
--   ORDER BY is_pinned DESC, pinned_at DESC NULLS LAST, updated_at DESC
CREATE INDEX IF NOT EXISTS idx_sessions_tenant_user_pin
    ON sessions (tenant_id, user_id, is_pinned DESC, pinned_at DESC, updated_at DESC)
    WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000039] sessions user_id/pin columns added'; END $$;

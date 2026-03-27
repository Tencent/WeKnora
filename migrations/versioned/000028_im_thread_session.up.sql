-- Migration: 000028_im_thread_session
-- Description: Add thread-based session mode support for IM channels
DO $$ BEGIN RAISE NOTICE '[Migration 000028] Adding thread session support'; END $$;

-- 1. im_channels: add session_mode column
ALTER TABLE im_channels
    ADD COLUMN IF NOT EXISTS session_mode VARCHAR(20) NOT NULL DEFAULT 'user';

-- 2. CHECK constraint (separate statement: ADD COLUMN IF NOT EXISTS does not support inline CONSTRAINT)
DO $$ BEGIN
    ALTER TABLE im_channels
        ADD CONSTRAINT chk_im_channels_session_mode
        CHECK (session_mode IN ('user', 'thread'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

COMMENT ON COLUMN im_channels.session_mode IS
    'Session resolution mode: user (per user+chat, default) or thread (per thread)';

-- 3. im_channel_sessions: add thread_id column
ALTER TABLE im_channel_sessions
    ADD COLUMN IF NOT EXISTS thread_id VARCHAR(128) NOT NULL DEFAULT '';

COMMENT ON COLUMN im_channel_sessions.thread_id IS
    'Platform thread identifier for thread-based sessions. Empty for user-mode sessions.';

-- 4. Unique index for thread-mode session lookup
--    Includes chat_id because Telegram message_thread_id is per-chat (topic ID within a group).
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id)
    WHERE deleted_at IS NULL AND thread_id != '';

DO $$ BEGIN RAISE NOTICE '[Migration 000028] Thread session support added'; END $$;

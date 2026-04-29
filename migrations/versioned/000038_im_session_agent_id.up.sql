-- Migration: 000038_im_session_agent_id
-- Description: Add agent_id to IM channel session unique indexes to isolate sessions per agent.
-- Fixes: GitHub #1066 (cross-agent session contamination)
DO $$ BEGIN RAISE NOTICE '[Migration 000038] Adding agent_id to IM channel session indexes'; END $$;

-- 1. User-mode lookup: include agent_id so the same user talking to
--    different agents gets separate sessions.
DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL;

-- 2. Thread-mode lookup: same fix for thread-based sessions.
DROP INDEX IF EXISTS idx_channel_thread_lookup;
CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL AND thread_id != '';

DO $$ BEGIN RAISE NOTICE '[Migration 000038] IM session agent_id indexes updated'; END $$;

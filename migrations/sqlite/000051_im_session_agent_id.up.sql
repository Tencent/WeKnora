-- Migration: 000051_im_session_agent_id
-- Description: Include agent_id in the IM channel session unique indexes so a
-- user can keep one session per agent. Mirrors versioned/000038, which only
-- covered PostgreSQL; SQLite still keyed sessions without agent_id and failed
-- with "UNIQUE constraint failed" after a channel switched its agent.
--
-- The new indexes are wider than the old ones, so existing rows can never
-- conflict and no data migration is required.

DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_channel_thread_lookup;
CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL AND thread_id != '';

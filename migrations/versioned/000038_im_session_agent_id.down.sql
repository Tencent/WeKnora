-- Rollback: 000038_im_session_agent_id
-- Restores the original indexes without agent_id.

DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_channel_thread_lookup;
CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id)
    WHERE deleted_at IS NULL AND thread_id != '';

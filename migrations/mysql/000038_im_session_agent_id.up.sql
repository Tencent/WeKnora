-- MySQL 8 translation of 000038_im_session_agent_id.
-- MySQL has no partial indexes. Soft-deleted rows remain in the uniqueness
-- domain; application queries already scope them out.
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions(platform, user_id, chat_id, tenant_id, agent_id);
CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions(platform, chat_id, thread_id, tenant_id, agent_id);

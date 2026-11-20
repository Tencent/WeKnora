-- Restoring the old constraint can fail after multiple threads have been
-- created by one user. Fail rather than delete or hide conversation mappings;
-- those conflicts must be resolved explicitly before retrying the rollback.
DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL;

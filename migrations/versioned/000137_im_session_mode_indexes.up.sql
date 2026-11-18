-- User and thread mappings have different identities. Applying the user
-- constraint to thread rows prevents the same user from opening a second
-- thread, and prevents user-mode and thread-mode mappings from coexisting.
-- This narrows the constraint without changing any existing mappings.
DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL AND thread_id = '';

-- Attachment cleanup checks references across live conversations, including
-- forks. Index only live user messages so each candidate can use JSONB
-- containment without scanning every message's attachments.
-- Keep this as a single statement: CONCURRENTLY cannot run in a transaction.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_messages_live_attachments
    ON messages USING gin (attachments jsonb_path_ops)
    WHERE role = 'user' AND deleted_at IS NULL;

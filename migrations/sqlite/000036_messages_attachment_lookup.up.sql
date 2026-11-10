-- SQLite expands live references once per cleanup query. Limit that scan to
-- live user messages and use session_id for the owning tenant/liveness join.
CREATE INDEX IF NOT EXISTS idx_messages_live_attachments
    ON messages (session_id) WHERE role = 'user' AND deleted_at IS NULL;

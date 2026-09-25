-- Mirrors versioned migration 000106_messages_session_created_index: the index
-- behind agent history's backwards read and the newest-checkpoint lookup.
CREATE INDEX IF NOT EXISTS idx_messages_session_created_id
    ON messages (session_id, created_at DESC, id DESC);

-- Mirrors versioned migration 000106_message_context_checkpoint_index: a
-- partial index for the per-turn lookup of a session's newest checkpoint.
CREATE INDEX IF NOT EXISTS idx_messages_session_context_checkpoint
    ON messages (session_id, created_at DESC, id DESC)
    WHERE context_checkpoint IS NOT NULL AND role = 'assistant';

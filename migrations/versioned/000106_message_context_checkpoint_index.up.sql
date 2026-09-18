-- Migration: 000106_message_context_checkpoint_index
-- LoadAgentHistory looks up a session's newest context checkpoint on every
-- agent turn. messages is indexed by session_id alone, so without this index
-- the lookup reads every message of the session to find the few assistant
-- rows that carry a checkpoint. The index is partial, so it only holds those
-- rows and stays small. GetLatestContextCheckpoint writes the role as a
-- literal so a prepared statement still matches the predicate.
--
-- CREATE INDEX holds a SHARE lock on messages while it scans the table. For a
-- very large messages table, operators may prefer to create the same index
-- with CONCURRENTLY from a separate script beforehand; IF NOT EXISTS then
-- makes this statement a no-op.
DO $$ BEGIN RAISE NOTICE '[Migration 000106] Creating idx_messages_session_context_checkpoint'; END $$;

CREATE INDEX IF NOT EXISTS idx_messages_session_context_checkpoint
    ON messages (session_id, created_at DESC, id DESC)
    WHERE context_checkpoint IS NOT NULL AND role = 'assistant';

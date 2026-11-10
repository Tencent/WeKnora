-- Mirrors versioned migration 000097_session_fork:
-- session lineage plus the per-turn sandbox checkpoint used as a fork point.
-- Existing Lite databases are already at version 13 and never replay 000000_init,
-- so these columns must be added here.

ALTER TABLE sessions ADD COLUMN parent_session_id TEXT;
ALTER TABLE sessions ADD COLUMN forked_from_message_id TEXT;
ALTER TABLE sessions ADD COLUMN fork_bootstrap TEXT;

CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id
    ON sessions (parent_session_id);

ALTER TABLE messages ADD COLUMN sandbox_checkpoint TEXT;

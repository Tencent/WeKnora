DROP INDEX IF EXISTS idx_sessions_parent_session_id;

ALTER TABLE sessions DROP COLUMN fork_bootstrap;
ALTER TABLE sessions DROP COLUMN forked_from_message_id;
ALTER TABLE sessions DROP COLUMN parent_session_id;

ALTER TABLE messages DROP COLUMN sandbox_checkpoint;

DROP INDEX IF EXISTS idx_sessions_unconsumed_fork;
DROP INDEX IF EXISTS idx_sessions_parent_session_id;

ALTER TABLE sessions DROP COLUMN IF EXISTS fork_bootstrap;
ALTER TABLE sessions DROP COLUMN IF EXISTS forked_from_message_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS parent_session_id;

ALTER TABLE messages DROP COLUMN IF EXISTS sandbox_checkpoint;

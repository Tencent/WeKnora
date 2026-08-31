DROP INDEX IF EXISTS idx_messages_feedback_id;
ALTER TABLE messages DROP COLUMN IF EXISTS feedback;
ALTER TABLE messages DROP COLUMN IF EXISTS feedback_id;

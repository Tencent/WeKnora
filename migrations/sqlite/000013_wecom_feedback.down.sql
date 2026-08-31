DROP INDEX IF EXISTS idx_messages_feedback_id;
ALTER TABLE messages DROP COLUMN feedback;
ALTER TABLE messages DROP COLUMN feedback_id;

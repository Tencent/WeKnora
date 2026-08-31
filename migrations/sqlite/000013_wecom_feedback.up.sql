-- Mirrors versioned migration 000091_wecom_feedback for Lite deployments.
ALTER TABLE messages ADD COLUMN feedback_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN feedback TEXT;
CREATE INDEX IF NOT EXISTS idx_messages_feedback_id ON messages(feedback_id);

-- Store the feedback identifier emitted in WeCom final reply frames and the
-- latest feedback_event payload received for the corresponding answer.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS feedback_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS feedback JSONB;

CREATE INDEX IF NOT EXISTS idx_messages_feedback_id
    ON messages (feedback_id)
    WHERE feedback_id <> '';

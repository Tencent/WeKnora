-- Migration: 000104_message_context_checkpoint
-- Agent compaction summaries persisted on the assistant message of the last
-- turn they cover, so the next turn's history starts from the summary instead
-- of summarizing the same turns again.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS context_checkpoint JSONB;

COMMENT ON COLUMN messages.context_checkpoint IS 'Agent compaction summary covering this turn and every turn before it';

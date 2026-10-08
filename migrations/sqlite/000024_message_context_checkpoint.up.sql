-- Mirrors versioned migration 000105_message_context_checkpoint: the agent
-- compaction summary persisted on the last turn it covers.
ALTER TABLE messages ADD COLUMN context_checkpoint TEXT;

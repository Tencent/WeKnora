-- Remove agent_duration_ms column from messages table
ALTER TABLE messages DROP COLUMN IF EXISTS agent_duration_ms;

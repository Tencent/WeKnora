-- Add agent_duration_ms column to messages table
-- Stores the total agent execution duration in milliseconds (from query start to answer start)
ALTER TABLE messages ADD COLUMN IF NOT EXISTS agent_duration_ms BIGINT DEFAULT 0;

-- Mirrors versioned migration 000085_message_usage:
-- per-turn LLM token usage persisted with the assistant message.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS usage TEXT;

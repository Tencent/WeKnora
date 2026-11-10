-- Migration: 000025_message_channel (rollback)
-- Description: Remove channel column from messages and knowledge
ALTER TABLE messages DROP COLUMN IF EXISTS channel;
ALTER TABLE knowledges DROP COLUMN IF EXISTS channel;

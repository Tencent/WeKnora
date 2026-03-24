-- Migration: 000025_message_channel (rollback)
-- Description: Remove channel column from messages
ALTER TABLE messages DROP COLUMN IF EXISTS channel;

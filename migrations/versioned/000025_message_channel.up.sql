-- Migration: 000025_message_channel
-- Description: Add channel column to messages to track the source channel (web, api, im, etc.)
DO $$ BEGIN RAISE NOTICE '[Migration 000025] Adding channel column to messages'; END $$;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS channel VARCHAR(50) NOT NULL DEFAULT '';

COMMENT ON COLUMN messages.channel IS 'Source channel of the message: web, api, im, etc.';

DO $$ BEGIN RAISE NOTICE '[Migration 000025] channel column added to messages successfully'; END $$;

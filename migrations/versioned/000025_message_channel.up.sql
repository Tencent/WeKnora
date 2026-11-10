-- Migration: 000025_message_channel
-- Description: Add channel column to messages and knowledge to track the source channel (web, api, im, etc.)
DO $$ BEGIN RAISE NOTICE '[Migration 000025] Adding channel column to messages and knowledge'; END $$;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS channel VARCHAR(50) NOT NULL DEFAULT '';
COMMENT ON COLUMN messages.channel IS 'Source channel of the message: web, api, im, etc.';

ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS channel VARCHAR(50) NOT NULL DEFAULT 'web';
COMMENT ON COLUMN knowledges.channel IS 'Source channel of the knowledge: web, api, browser_extension, wechat, etc.';

DO $$ BEGIN RAISE NOTICE '[Migration 000025] channel column added to messages and knowledge successfully'; END $$;

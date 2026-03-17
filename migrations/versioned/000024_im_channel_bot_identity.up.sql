-- Migration: 000024_im_channel_bot_identity
-- Description: Add bot_identity column to im_channels for duplicate bot prevention.
-- bot_identity is a computed unique key derived from credentials, ensuring each bot
-- can only be connected to one active (non-deleted) channel.
DO $$ BEGIN RAISE NOTICE '[Migration 000024] Adding bot_identity column to im_channels'; END $$;

ALTER TABLE im_channels ADD COLUMN IF NOT EXISTS bot_identity VARCHAR(255) NOT NULL DEFAULT '';

-- Partial unique index: only enforce uniqueness for non-deleted rows with a non-empty bot_identity.
-- Empty bot_identity (unknown credential format) is excluded from the constraint.
CREATE UNIQUE INDEX IF NOT EXISTS idx_im_channels_bot_identity
    ON im_channels (bot_identity)
    WHERE deleted_at IS NULL AND bot_identity != '';

COMMENT ON COLUMN im_channels.bot_identity IS 'Unique bot identity derived from credentials (e.g. wecom:ws:{bot_id}, feishu:{app_id}). Used to prevent duplicate bot bindings.';

DO $$ BEGIN RAISE NOTICE '[Migration 000024] bot_identity column and unique index created successfully'; END $$;

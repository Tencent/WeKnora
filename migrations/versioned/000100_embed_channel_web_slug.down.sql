-- Migration: 000079_embed_channel_web_slug (down)

DROP INDEX IF EXISTS idx_embed_channels_web_slug;

ALTER TABLE embed_channels
    DROP COLUMN IF EXISTS web_slug;

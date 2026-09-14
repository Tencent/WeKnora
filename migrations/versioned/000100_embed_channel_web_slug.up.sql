-- Migration: 000079_embed_channel_web_slug
-- Short public slug for direct-open web chat links (/w/:slug).

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Adding web_slug to embed_channels...'; END $$;

ALTER TABLE embed_channels
    ADD COLUMN IF NOT EXISTS web_slug VARCHAR(16) NOT NULL DEFAULT '';

-- Backfill unique short slugs for existing channels (hex of uuid prefix).
UPDATE embed_channels
SET web_slug = lower(substr(replace(id::text, '-', ''), 1, 10))
WHERE web_slug = '' AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_embed_channels_web_slug
    ON embed_channels (web_slug)
    WHERE web_slug <> '' AND deleted_at IS NULL;

COMMENT ON COLUMN embed_channels.web_slug IS
    'Short public code for direct web chat URLs (/w/:slug); not the publish token';

DO $$ BEGIN RAISE NOTICE '[Migration 000079] web_slug ready'; END $$;

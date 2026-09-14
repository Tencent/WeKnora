-- Migration: 000007_embed_channel_web_slug
-- Short public slug for direct-open web chat links (/w/:slug).

ALTER TABLE embed_channels ADD COLUMN web_slug TEXT NOT NULL DEFAULT '';

UPDATE embed_channels
SET web_slug = lower(substr(replace(id, '-', ''), 1, 10))
WHERE web_slug = '' AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_embed_channels_web_slug
    ON embed_channels (web_slug)
    WHERE web_slug != '' AND deleted_at IS NULL;

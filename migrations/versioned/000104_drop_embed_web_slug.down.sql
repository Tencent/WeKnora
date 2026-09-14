-- Migration: 000083_drop_embed_web_slug (down)

ALTER TABLE embed_channels
    ADD COLUMN IF NOT EXISTS web_slug VARCHAR(16) NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_embed_channels_web_slug
    ON embed_channels (web_slug)
    WHERE web_slug <> '' AND deleted_at IS NULL;

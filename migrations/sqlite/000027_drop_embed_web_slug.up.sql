-- Migration: 000011_drop_embed_web_slug
-- (align with versioned 000083)

DROP INDEX IF EXISTS idx_embed_channels_web_slug;
ALTER TABLE embed_channels DROP COLUMN web_slug;

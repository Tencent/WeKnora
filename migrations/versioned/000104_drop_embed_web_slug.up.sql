DROP INDEX IF EXISTS idx_embed_channels_web_slug;
ALTER TABLE embed_channels DROP COLUMN IF EXISTS web_slug;

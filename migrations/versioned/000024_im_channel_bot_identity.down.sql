DROP INDEX IF EXISTS idx_im_channels_bot_identity;
ALTER TABLE im_channels DROP COLUMN IF EXISTS bot_identity;

ALTER TABLE im_channel_sessions DROP COLUMN IF EXISTS last_detected_language;
ALTER TABLE im_channels DROP COLUMN IF EXISTS language_mode;

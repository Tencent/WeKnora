ALTER TABLE im_channels
    ADD COLUMN IF NOT EXISTS language_mode VARCHAR(20) NOT NULL DEFAULT 'fixed';

ALTER TABLE im_channel_sessions
    ADD COLUMN IF NOT EXISTS last_detected_language VARCHAR(64) NOT NULL DEFAULT '';

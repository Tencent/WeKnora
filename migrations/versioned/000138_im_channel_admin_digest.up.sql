ALTER TABLE im_channels
    ADD COLUMN IF NOT EXISTS admin_digest JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN im_channels.admin_digest IS
    'Admin digest config for this IM channel: {"enabled": bool, "user_ids": [platform user ids]}; disabled by default';

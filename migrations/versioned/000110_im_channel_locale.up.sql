ALTER TABLE im_channels
    ADD COLUMN IF NOT EXISTS locale VARCHAR(16) NOT NULL DEFAULT '';

COMMENT ON COLUMN im_channels.locale IS
    'Fixed locale for responses on this IM channel; empty uses the deployment default';

-- Migration 000122: IM platform IDs of plugins are qualified contribution IDs
-- ("acme.zulip/zulip"), longer than the builtin platform names.
DO $$ BEGIN RAISE NOTICE '[Migration 000122] Widening IM platform columns'; END $$;

ALTER TABLE im_channels ALTER COLUMN platform TYPE VARCHAR(160);
ALTER TABLE im_channel_sessions ALTER COLUMN platform TYPE VARCHAR(160);

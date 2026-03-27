DO $$ BEGIN RAISE NOTICE '[Migration 000028] Rolling back thread session support'; END $$;

DROP INDEX IF EXISTS idx_channel_thread_lookup;
ALTER TABLE im_channel_sessions DROP COLUMN IF EXISTS thread_id;
ALTER TABLE im_channels DROP CONSTRAINT IF EXISTS chk_im_channels_session_mode;
ALTER TABLE im_channels DROP COLUMN IF EXISTS session_mode;

DO $$ BEGIN RAISE NOTICE '[Migration 000028] Rollback completed'; END $$;

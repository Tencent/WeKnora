DO $$ BEGIN RAISE NOTICE '[Migration 000061] Adding embed channel appearance columns'; END $$;

ALTER TABLE embed_channels
    ADD COLUMN IF NOT EXISTS primary_color VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS page_title VARCHAR(255) NOT NULL DEFAULT '';

COMMENT ON COLUMN embed_channels.primary_color IS 'CSS color for embed widget accent (e.g. #0052d9)';
COMMENT ON COLUMN embed_channels.page_title IS 'Browser tab title for the embed page';

DO $$ BEGIN RAISE NOTICE '[Migration 000061] Done'; END $$;

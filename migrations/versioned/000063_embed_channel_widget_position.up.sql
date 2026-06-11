DO $$ BEGIN RAISE NOTICE '[Migration 000063] Adding embed channel widget_position'; END $$;

ALTER TABLE embed_channels
    ADD COLUMN IF NOT EXISTS widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right';

COMMENT ON COLUMN embed_channels.widget_position IS 'Floating widget corner: bottom-right, bottom-left, top-right, top-left';

DO $$ BEGIN RAISE NOTICE '[Migration 000063] Done'; END $$;

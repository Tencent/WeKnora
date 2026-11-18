-- Migration: 000138_embed_channel_visibility_switches (down)
DO $$ BEGIN RAISE NOTICE '[Migration 000138] Dropping embed channel visibility switches'; END $$;

ALTER TABLE embed_channels
    DROP COLUMN IF EXISTS show_tool_steps,
    DROP COLUMN IF EXISTS show_sources;

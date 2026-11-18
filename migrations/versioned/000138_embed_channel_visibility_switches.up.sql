-- Migration: 000138_embed_channel_visibility_switches
-- Channel-level switches controlling whether anonymous embed pages render
-- the tool-call/retrieval step tree and the source-document cards (#5631).
DO $$ BEGIN RAISE NOTICE '[Migration 000138] Adding embed channel visibility switches'; END $$;

ALTER TABLE embed_channels
    ADD COLUMN IF NOT EXISTS show_tool_steps BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS show_sources BOOLEAN NOT NULL DEFAULT true;

COMMENT ON COLUMN embed_channels.show_tool_steps IS 'Render the tool-call/retrieval step tree on the embed page';
COMMENT ON COLUMN embed_channels.show_sources IS 'Render the source-document cards on the embed page';

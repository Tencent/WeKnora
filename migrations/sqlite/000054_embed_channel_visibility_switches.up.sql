-- Migration: 000054_embed_channel_visibility_switches
-- Channel-level switches for embed page rendering of steps and sources (#5631).
ALTER TABLE embed_channels ADD COLUMN show_tool_steps INTEGER NOT NULL DEFAULT 1;
ALTER TABLE embed_channels ADD COLUMN show_sources INTEGER NOT NULL DEFAULT 1;

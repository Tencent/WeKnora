-- Migration: 000124_add_video_config
-- Description: Add video_config column to knowledge_bases for video parsing.
-- audio_only is the only implemented mode; the column is JSON rather than
-- separate booleans so padding the mode later does not need a schema change.
DO $$ BEGIN RAISE NOTICE '[Migration 000124] Adding video_config column to knowledge_bases'; END $$;

ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS video_config JSONB
    DEFAULT '{"enabled": false, "mode": "audio_only"}'::jsonb;

COMMENT ON COLUMN knowledge_bases.video_config IS
    'Video parsing configuration: {"enabled": bool, "mode": "audio_only"}';

DO $$ BEGIN RAISE NOTICE '[Migration 000124] video_config column added successfully'; END $$;

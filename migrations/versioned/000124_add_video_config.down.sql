-- Migration: 000124_add_video_config (rollback)
-- Description: Remove video_config column from knowledge_bases.
DO $$ BEGIN RAISE NOTICE '[Migration 000124] Removing video_config column from knowledge_bases'; END $$;

ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS video_config;

DO $$ BEGIN RAISE NOTICE '[Migration 000124] video_config column removed successfully'; END $$;

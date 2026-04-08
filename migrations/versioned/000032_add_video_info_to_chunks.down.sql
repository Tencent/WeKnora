-- Migration: 000032_add_video_info_to_chunks (down)
-- Description: Remove video_info column from chunks table.
DO $$ BEGIN RAISE NOTICE '[Migration 000032 down] Removing video_info column from chunks'; END $$;

ALTER TABLE chunks DROP COLUMN IF EXISTS video_info;

DO $$ BEGIN RAISE NOTICE '[Migration 000032 down] video_info column removed successfully'; END $$;

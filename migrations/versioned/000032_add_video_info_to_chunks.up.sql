-- Migration: 000032_add_video_info_to_chunks
-- Description: Add video_info column to chunks table for video multimodal processing.
-- This column stores JSON-serialized VideoInfo struct containing video analysis results.
DO $$ BEGIN RAISE NOTICE '[Migration 000032] Adding video_info column to chunks'; END $$;

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS video_info TEXT;

COMMENT ON COLUMN chunks.video_info IS 'Video information in JSON format: {"url": string, "frame_count": int, "has_vlm_analysis": bool, "has_asr": bool, "video_summary": string, "asr_text": string, "frame_descriptions": string[]}';

DO $$ BEGIN RAISE NOTICE '[Migration 000032] video_info column added successfully'; END $$;

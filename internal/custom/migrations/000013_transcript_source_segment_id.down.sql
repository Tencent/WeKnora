DROP INDEX IF EXISTS idx_video_transcript_chunks_source_segment;

ALTER TABLE video_transcript_chunks
    DROP COLUMN IF EXISTS source_segment_id;

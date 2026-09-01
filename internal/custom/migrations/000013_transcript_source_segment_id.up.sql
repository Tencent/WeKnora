ALTER TABLE video_transcript_chunks
    ADD COLUMN IF NOT EXISTS source_segment_id VARCHAR(192) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_video_transcript_chunks_source_segment
    ON video_transcript_chunks (video_id, generation, source_segment_id);

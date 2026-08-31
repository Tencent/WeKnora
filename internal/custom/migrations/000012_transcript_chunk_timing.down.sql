ALTER TABLE video_transcript_chunks
    DROP COLUMN IF EXISTS start_ms,
    DROP COLUMN IF EXISTS end_ms;

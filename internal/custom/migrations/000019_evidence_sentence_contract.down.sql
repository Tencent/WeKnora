DROP INDEX IF EXISTS idx_video_transcript_chunks_evidence_sentence;

ALTER TABLE video_transcript_chunks
    DROP COLUMN IF EXISTS evidence_sentence_id,
    DROP COLUMN IF EXISTS speaker_id;

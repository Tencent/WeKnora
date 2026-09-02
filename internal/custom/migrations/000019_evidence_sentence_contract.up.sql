ALTER TABLE video_transcript_chunks
    ADD COLUMN IF NOT EXISTS evidence_sentence_id VARCHAR(192) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS speaker_id VARCHAR(128) NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_video_transcript_chunks_evidence_sentence
    ON video_transcript_chunks (video_id, generation, evidence_sentence_id)
    WHERE evidence_sentence_id <> '';

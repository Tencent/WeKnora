DROP INDEX IF EXISTS idx_video_transcript_sources_knowledge_base;

ALTER TABLE video_transcript_sources
    DROP CONSTRAINT IF EXISTS uq_video_transcript_sources_video_generation_kb;

DELETE FROM video_transcript_sources
WHERE id NOT IN (
    SELECT MIN(id)
    FROM video_transcript_sources
    GROUP BY video_id, transcript_generation
);

ALTER TABLE video_transcript_sources
    ADD CONSTRAINT uq_video_transcript_sources_video_generation
    UNIQUE (video_id, transcript_generation);

ALTER TABLE video_transcript_sources
    DROP COLUMN IF EXISTS knowledge_base_id;

ALTER TABLE video_transcript_sources
    ADD COLUMN IF NOT EXISTS knowledge_base_id VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE video_transcript_sources
    DROP CONSTRAINT IF EXISTS uq_video_transcript_sources_video_generation;

ALTER TABLE video_transcript_sources
    ADD CONSTRAINT uq_video_transcript_sources_video_generation_kb
    UNIQUE (video_id, transcript_generation, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_video_transcript_sources_knowledge_base
    ON video_transcript_sources(knowledge_base_id);

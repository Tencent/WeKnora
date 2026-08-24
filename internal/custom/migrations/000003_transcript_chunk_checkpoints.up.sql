ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS transcript_generation VARCHAR(64),
    ADD COLUMN IF NOT EXISTS transcript_revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS transcript_active_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS video_transcript_chunks (
    video_id VARCHAR(36) NOT NULL,
    generation VARCHAR(64) NOT NULL,
    revision BIGINT NOT NULL,
    chunk_index INTEGER NOT NULL,
    knowledge_id VARCHAR(64) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'created',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (video_id, generation, chunk_index),
    UNIQUE (knowledge_id),
    CONSTRAINT fk_transcript_chunks_video FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_transcript_chunks_video_status
    ON video_transcript_chunks(video_id, revision, generation, status);

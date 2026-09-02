CREATE TABLE IF NOT EXISTS video_transcript_sources (
    id VARCHAR(36) PRIMARY KEY,
    video_id VARCHAR(36) NOT NULL,
    transcript_generation VARCHAR(64) NOT NULL,
    knowledge_id VARCHAR(64) NOT NULL DEFAULT '',
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_video_transcript_sources_video_generation UNIQUE (video_id, transcript_generation)
);

CREATE INDEX IF NOT EXISTS idx_video_transcript_sources_status
    ON video_transcript_sources(status);

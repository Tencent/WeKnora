CREATE TABLE IF NOT EXISTS wiki_relation_audits (
    id VARCHAR(36) PRIMARY KEY,
    video_id VARCHAR(36) NOT NULL,
    transcript_generation VARCHAR(64) NOT NULL,
    source_wiki_page_id VARCHAR(64) NOT NULL,
    source_object_id VARCHAR(128) NOT NULL,
    relation_id VARCHAR(128),
    relation_type VARCHAR(64),
    target_object_id VARCHAR(128),
    target_wiki_page_id VARCHAR(64),
    evidence_ids TEXT NOT NULL DEFAULT '[]',
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wiki_relation_audits_video_generation
    ON wiki_relation_audits(video_id, transcript_generation);

CREATE INDEX IF NOT EXISTS idx_wiki_relation_audits_status
    ON wiki_relation_audits(status);

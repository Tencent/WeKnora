CREATE TABLE IF NOT EXISTS wiki_identity_audits (
    id VARCHAR(36) PRIMARY KEY,
    video_id VARCHAR(36) NOT NULL,
    transcript_generation VARCHAR(64) NOT NULL,
    source_wiki_page_id VARCHAR(64) NOT NULL,
    source_object_id VARCHAR(128) NOT NULL,
    candidate_wiki_page_id VARCHAR(64) NOT NULL,
    candidate_object_id VARCHAR(128) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    candidate_type VARCHAR(32) NOT NULL,
    source_entity_sub_type VARCHAR(32),
    candidate_entity_sub_type VARCHAR(32),
    type_match BOOLEAN NOT NULL DEFAULT FALSE,
    title_match BOOLEAN NOT NULL DEFAULT FALSE,
    context_match BOOLEAN NOT NULL DEFAULT FALSE,
    evidence_overlap BOOLEAN NOT NULL DEFAULT FALSE,
    score DOUBLE PRECISION NOT NULL DEFAULT 0,
    decision VARCHAR(32) NOT NULL,
    reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wiki_identity_audits_video_generation
    ON wiki_identity_audits(video_id, transcript_generation);

CREATE INDEX IF NOT EXISTS idx_wiki_identity_audits_decision
    ON wiki_identity_audits(decision);

CREATE INDEX IF NOT EXISTS idx_wiki_identity_audits_normalized_name
    ON wiki_identity_audits(normalized_name);

CREATE TABLE IF NOT EXISTS chat_source_audits (
    id VARCHAR(36) PRIMARY KEY,
    event_id VARCHAR(128) NOT NULL,
    session_id VARCHAR(64),
    scope VARCHAR(16),
    video_id VARCHAR(36),
    source_mode VARCHAR(32),
    fallback_used BOOLEAN NOT NULL DEFAULT FALSE,
    references_found INTEGER NOT NULL DEFAULT 0,
    wiki_page_ids TEXT NOT NULL DEFAULT '[]',
    knowledge_object_ids TEXT NOT NULL DEFAULT '[]',
    transcript_chunk_ids TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_source_audits_event_id
    ON chat_source_audits(event_id);

CREATE INDEX IF NOT EXISTS idx_chat_source_audits_session_id
    ON chat_source_audits(session_id);

CREATE INDEX IF NOT EXISTS idx_chat_source_audits_video_id
    ON chat_source_audits(video_id);

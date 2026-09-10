-- Embedding resume support (SQLite / lite mode).
-- Mirrors migrations/versioned/000091_embedding_resume.up.sql.

ALTER TABLE knowledges ADD COLUMN chunk_fingerprint VARCHAR(64) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS knowledge_embed_progress (
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id     VARCHAR(36) NOT NULL,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, chunk_id)
);

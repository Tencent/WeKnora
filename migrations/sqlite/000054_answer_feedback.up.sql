-- Match versioned/000138_answer_feedback for Lite mode.

ALTER TABLE chunks ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN approval_rate REAL;
ALTER TABLE chunks ADD COLUMN recall_weight REAL NOT NULL DEFAULT 1;
ALTER TABLE chunks ADD COLUMN needs_optimization INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_chunks_kb_feedback ON chunks (knowledge_base_id, approval_rate);

CREATE TABLE IF NOT EXISTS answer_chunk_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_chunk_link ON answer_chunk_links (message_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_answer_chunk_links_chunk ON answer_chunk_links (chunk_id);
CREATE INDEX IF NOT EXISTS idx_answer_chunk_links_session ON answer_chunk_links (session_id);

CREATE TABLE IF NOT EXISTS answer_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    rating VARCHAR(16) NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_feedback_user ON answer_feedbacks (message_id, user_id);
CREATE INDEX IF NOT EXISTS idx_answer_feedbacks_session ON answer_feedbacks (session_id);

CREATE TABLE IF NOT EXISTS chunk_feedback_attributions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    feedback_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    rating VARCHAR(16) NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chunk_feedback_attr ON chunk_feedback_attributions (feedback_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_attr_chunk ON chunk_feedback_attributions (chunk_id);

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    old_weight REAL NOT NULL,
    new_weight REAL NOT NULL,
    old_approval_rate REAL,
    new_approval_rate REAL,
    like_count INTEGER NOT NULL,
    dislike_count INTEGER NOT NULL,
    trigger_source VARCHAR(32) NOT NULL,
    message_id VARCHAR(36) NOT NULL DEFAULT '',
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk ON chunk_weight_logs (chunk_id, created_at);

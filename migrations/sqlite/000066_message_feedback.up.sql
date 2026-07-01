ALTER TABLE chunks ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN positive_rate REAL;
ALTER TABLE chunks ADD COLUMN recall_weight REAL NOT NULL DEFAULT 1.0;
ALTER TABLE chunks ADD COLUMN needs_optimization BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN feedback_reset_at DATETIME;

CREATE TABLE IF NOT EXISTS message_chunk_refs (
    id VARCHAR(36) PRIMARY KEY,
    session_tenant_id BIGINT NOT NULL,
    chunk_tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    chunk_type VARCHAR(32) NOT NULL DEFAULT '',
    match_type INTEGER NOT NULL DEFAULT 0,
    score REAL NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_tenant_id, message_id, chunk_id)
);

CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_message_id ON message_chunk_refs(message_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_chunk_id ON message_chunk_refs(chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_session_id ON message_chunk_refs(session_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_kb_id ON message_chunk_refs(knowledge_base_id);

CREATE TABLE IF NOT EXISTS message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    session_tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    feedback_type VARCHAR(16) NOT NULL DEFAULT 'none',
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    reason_text TEXT NOT NULL DEFAULT '',
    feedback_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_tenant_id, user_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_message_feedbacks_user_id ON message_feedbacks(user_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_session_id ON message_feedbacks(session_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_message_id ON message_feedbacks(message_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_feedback_at ON message_feedbacks(feedback_at);

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY,
    chunk_tenant_id BIGINT NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    old_weight REAL NOT NULL,
    new_weight REAL NOT NULL,
    source VARCHAR(32) NOT NULL,
    source_message_id VARCHAR(36) NOT NULL DEFAULT '',
    source_feedback_id VARCHAR(36) NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk_created ON chunk_weight_logs(chunk_tenant_id, chunk_id, created_at);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_source_message ON chunk_weight_logs(source_message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_source_feedback ON chunk_weight_logs(source_feedback_id);

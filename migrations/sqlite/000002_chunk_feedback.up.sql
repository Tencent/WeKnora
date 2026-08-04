-- SQLite migration 000002: Q&A feedback accumulation onto knowledge chunks.
ALTER TABLE chunks ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN approval_rate REAL NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN recall_weight REAL NOT NULL DEFAULT 1;
ALTER TABLE chunks ADD COLUMN needs_optimization BOOLEAN NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS message_chunk_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    chunk_seq_id INTEGER NOT NULL DEFAULT 0,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_title VARCHAR(512) NOT NULL DEFAULT '',
    chunk_content TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_chunk_links_message_chunk ON message_chunk_links (message_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_links_chunk ON message_chunk_links (chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_links_tenant_chunk ON message_chunk_links (tenant_id, chunk_id);

CREATE TABLE IF NOT EXISTS chunk_feedback_records (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    rating VARCHAR(16) NOT NULL,
    reason VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chunk_feedback_records_user_message ON chunk_feedback_records (user_id, message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_records_message ON chunk_feedback_records (message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_records_tenant_created ON chunk_feedback_records (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    old_weight REAL NOT NULL DEFAULT 1,
    new_weight REAL NOT NULL DEFAULT 1,
    source VARCHAR(24) NOT NULL DEFAULT 'feedback',
    message_id VARCHAR(36) NOT NULL DEFAULT '',
    user_id VARCHAR(36) NOT NULL DEFAULT '',
    reason VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk ON chunk_weight_logs (chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_created ON chunk_weight_logs (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS chunk_feedback_configs (
    tenant_id INTEGER PRIMARY KEY,
    boost_threshold REAL NOT NULL DEFAULT 0.8,
    degrade_threshold REAL NOT NULL DEFAULT 0.5,
    optimize_threshold REAL NOT NULL DEFAULT 0.4,
    min_votes INTEGER NOT NULL DEFAULT 1,
    weight_step REAL NOT NULL DEFAULT 0.1,
    max_weight REAL NOT NULL DEFAULT 2.0,
    min_weight REAL NOT NULL DEFAULT 0.1,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
-- SQLite (Lite mode) addition for answer feedback, chunk attribution,
-- recall-weight audit and chunk aggregate columns.

-- Answer -> chunk reference facts, written when an assistant message
-- completes. Independent from user feedback so per-chunk session stats
-- count every answer that cited the chunk, not only rated ones.
CREATE TABLE IF NOT EXISTS message_chunk_references (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_base_id VARCHAR(36) NOT NULL,
    is_sub_chunk INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_msg_chunk_refs_message_chunk
    ON message_chunk_references(message_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_refs_chunk
    ON message_chunk_references(chunk_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_refs_kb
    ON message_chunk_references(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_refs_tenant_chunk
    ON message_chunk_references(tenant_id, chunk_id);

-- One rating per (message, user). tenant_id is the evaluator's tenant.
CREATE TABLE IF NOT EXISTS message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(512) NOT NULL DEFAULT '',
    rating VARCHAR(16) NOT NULL,
    reasons TEXT NOT NULL DEFAULT '[]',
    comment TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_feedbacks_message_user
    ON message_feedbacks(message_id, user_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_tenant_session
    ON message_feedbacks(tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_message
    ON message_feedbacks(message_id);

-- Recall-weight change audit. tenant_id is the chunk owner's tenant.
CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    old_weight REAL NOT NULL,
    new_weight REAL NOT NULL,
    positive_rate REAL NOT NULL,
    trigger_source VARCHAR(16) NOT NULL,
    feedback_id VARCHAR(36),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_kb_chunk
    ON chunk_weight_logs(knowledge_base_id, chunk_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_kb
    ON chunk_weight_logs(tenant_id, knowledge_base_id);

ALTER TABLE chunks ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN positive_rate REAL NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN recall_weight REAL NOT NULL DEFAULT 1;

-- Feedback epoch: ratings updated before this instant no longer count
-- toward chunk stats after an admin reset.
ALTER TABLE knowledge_bases ADD COLUMN feedback_reset_at DATETIME;
ALTER TABLE knowledge_bases ADD COLUMN feedback_reset_by VARCHAR(64);
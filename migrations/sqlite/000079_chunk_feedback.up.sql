ALTER TABLE messages ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE chunks ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN positive_rate REAL NOT NULL DEFAULT 0.0;
ALTER TABLE chunks ADD COLUMN recall_weight REAL NOT NULL DEFAULT 1.0;
ALTER TABLE chunks ADD COLUMN quality_status VARCHAR(50) NOT NULL DEFAULT 'normal';
ALTER TABLE chunks ADD COLUMN dislike_reasons TEXT DEFAULT '[]';
ALTER TABLE chunks ADD COLUMN last_feedback_at DATETIME;

CREATE INDEX IF NOT EXISTS idx_chunks_quality_status ON chunks(quality_status);
CREATE INDEX IF NOT EXISTS idx_chunks_positive_rate ON chunks(positive_rate);
CREATE INDEX IF NOT EXISTS idx_chunks_recall_weight ON chunks(recall_weight);
CREATE INDEX IF NOT EXISTS idx_chunks_last_feedback_at ON chunks(last_feedback_at);
CREATE INDEX IF NOT EXISTS idx_messages_feedback ON messages(like_count, dislike_count);

CREATE TABLE IF NOT EXISTS qa_reply_chunk_refs (
    id VARCHAR(36) PRIMARY KEY,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    chunk_tenant_id INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, message_id, chunk_id, chunk_tenant_id),
    FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
    FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_message_id ON qa_reply_chunk_refs(message_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_chunk_id ON qa_reply_chunk_refs(chunk_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_tenant_id ON qa_reply_chunk_refs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_chunk_tenant_id ON qa_reply_chunk_refs(chunk_tenant_id);

CREATE TABLE IF NOT EXISTS qa_reply_chunk_ref_tombstones (
    id VARCHAR(36) PRIMARY KEY,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    chunk_tenant_id INTEGER NOT NULL,
    operator VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, message_id, chunk_id, chunk_tenant_id),
    FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
    FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_message_id ON qa_reply_chunk_ref_tombstones(message_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_chunk_id ON qa_reply_chunk_ref_tombstones(chunk_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_tenant_id ON qa_reply_chunk_ref_tombstones(tenant_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_chunk_tenant_id ON qa_reply_chunk_ref_tombstones(chunk_tenant_id);

CREATE TABLE IF NOT EXISTS chunk_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    message_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(512),
    is_positive BOOLEAN NOT NULL DEFAULT 1,
    dislike_reason VARCHAR(255),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, message_id, user_id),
    FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_message_id ON chunk_feedbacks(message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_session_id ON chunk_feedbacks(session_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_tenant_id ON chunk_feedbacks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_user_id ON chunk_feedbacks(user_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_created_at ON chunk_feedbacks(created_at);

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY,
    chunk_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    action VARCHAR(50) NOT NULL,
    old_weight REAL NOT NULL DEFAULT 1.0,
    new_weight REAL NOT NULL DEFAULT 1.0,
    trigger_type VARCHAR(50) NOT NULL,
    trigger_detail VARCHAR(500),
    operator VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk_id ON chunk_weight_logs(chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_id ON chunk_weight_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_action ON chunk_weight_logs(action);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_trigger_type ON chunk_weight_logs(trigger_type);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_created_at ON chunk_weight_logs(created_at);

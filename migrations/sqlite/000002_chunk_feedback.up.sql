ALTER TABLE chunks ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0 CHECK (like_count >= 0);
ALTER TABLE chunks ADD COLUMN dislike_count INTEGER NOT NULL DEFAULT 0 CHECK (dislike_count >= 0);
ALTER TABLE chunks ADD COLUMN positive_rate REAL CHECK (positive_rate IS NULL OR (positive_rate >= 0 AND positive_rate <= 1));
ALTER TABLE chunks ADD COLUMN recall_weight REAL NOT NULL DEFAULT 1.0 CHECK (recall_weight > 0);
ALTER TABLE chunks ADD COLUMN feedback_reset_at DATETIME;

CREATE TABLE message_chunk_references (
    id VARCHAR(36) PRIMARY KEY,
    message_tenant_id INTEGER NOT NULL,
    chunk_tenant_id INTEGER NOT NULL,
    chunk_knowledge_base_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (message_tenant_id, message_id, chunk_tenant_id, chunk_knowledge_base_id, chunk_id)
);
CREATE INDEX idx_message_reference_message ON message_chunk_references (message_tenant_id, message_id);
CREATE INDEX idx_message_reference_chunk
    ON message_chunk_references (chunk_tenant_id, chunk_knowledge_base_id, chunk_id);

CREATE TABLE message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    feedback_type VARCHAR(16) NOT NULL CHECK (feedback_type IN ('like', 'dislike')),
    reason_code VARCHAR(16) CHECK (
        (feedback_type = 'like' AND reason_code IS NULL)
        OR
        (feedback_type = 'dislike' AND reason_code IS NOT NULL
            AND reason_code IN ('inaccurate', 'irrelevant', 'incomplete', 'outdated', 'other'))
    ),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, user_id, message_id)
);
CREATE INDEX idx_message_feedback_session ON message_feedbacks (tenant_id, session_id);
CREATE INDEX idx_message_feedback_message ON message_feedbacks (tenant_id, message_id);

CREATE TABLE chunk_feedback_audits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chunk_tenant_id INTEGER NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    actor_tenant_id INTEGER NOT NULL,
    actor_user_id VARCHAR(64) NOT NULL,
    action VARCHAR(32) NOT NULL CHECK (action IN ('feedback_weight_changed', 'feedback_reset')),
    trigger_source VARCHAR(16) NOT NULL DEFAULT 'legacy' CHECK (
        trigger_source IN ('like', 'dislike', 'cancel', 'admin_reset', 'content_delete', 'legacy')
    ),
    old_weight REAL NOT NULL,
    new_weight REAL NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_chunk_feedback_audit_chunk ON chunk_feedback_audits (chunk_tenant_id, chunk_id, created_at DESC);

CREATE TABLE IF NOT EXISTS message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    feedback_type VARCHAR(20) NOT NULL,
    reason TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_feedbacks_message ON message_feedbacks(message_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_tenant ON message_feedbacks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_session ON message_feedbacks(session_id);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_type ON message_feedbacks(feedback_type);

-- Minimal message-to-chunk feedback loop.
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS positive_rate DOUBLE PRECISION;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1.0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS feedback_reset_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE chunks ADD CONSTRAINT chk_chunks_feedback_counts CHECK (like_count >= 0 AND dislike_count >= 0);
ALTER TABLE chunks ADD CONSTRAINT chk_chunks_positive_rate CHECK (positive_rate IS NULL OR (positive_rate >= 0 AND positive_rate <= 1));
ALTER TABLE chunks ADD CONSTRAINT chk_chunks_recall_weight CHECK (recall_weight >= 0.8 AND recall_weight <= 1.2);

CREATE TABLE message_chunk_references (
    id VARCHAR(36) PRIMARY KEY,
    message_tenant_id BIGINT NOT NULL,
    chunk_tenant_id BIGINT NOT NULL,
    chunk_knowledge_base_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_message_chunk_reference UNIQUE (
        message_tenant_id, message_id, chunk_tenant_id, chunk_knowledge_base_id, chunk_id
    )
);
CREATE INDEX idx_message_reference_message ON message_chunk_references (message_tenant_id, message_id);
CREATE INDEX idx_message_reference_chunk
    ON message_chunk_references (chunk_tenant_id, chunk_knowledge_base_id, chunk_id);

CREATE TABLE message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    feedback_type VARCHAR(16) NOT NULL,
    reason_code VARCHAR(16),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_message_feedback_actor UNIQUE (tenant_id, user_id, message_id),
    CONSTRAINT chk_message_feedback_type CHECK (feedback_type IN ('like', 'dislike')),
    CONSTRAINT chk_message_feedback_reason CHECK (
        (feedback_type = 'like' AND reason_code IS NULL)
        OR
        (feedback_type = 'dislike' AND reason_code IS NOT NULL
            AND reason_code IN ('inaccurate', 'irrelevant', 'incomplete', 'outdated', 'other'))
    )
);
CREATE INDEX idx_message_feedback_session ON message_feedbacks (tenant_id, session_id);
CREATE INDEX idx_message_feedback_message ON message_feedbacks (tenant_id, message_id);

CREATE TABLE chunk_feedback_audits (
    id BIGSERIAL PRIMARY KEY,
    chunk_tenant_id BIGINT NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    actor_tenant_id BIGINT NOT NULL,
    actor_user_id VARCHAR(64) NOT NULL,
    action VARCHAR(32) NOT NULL,
    trigger_source VARCHAR(16) NOT NULL DEFAULT 'legacy',
    old_weight DOUBLE PRECISION NOT NULL,
    new_weight DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_chunk_feedback_audit_action CHECK (action IN ('feedback_weight_changed', 'feedback_reset')),
    CONSTRAINT chk_chunk_feedback_audit_trigger_source CHECK (
        trigger_source IN ('like', 'dislike', 'cancel', 'admin_reset', 'content_delete', 'legacy')
    )
);
CREATE INDEX idx_chunk_feedback_audit_chunk ON chunk_feedback_audits (chunk_tenant_id, chunk_id, created_at DESC);

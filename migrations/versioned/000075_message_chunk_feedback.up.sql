-- Migration: 000075_message_chunk_feedback
-- Description: Attribute answer feedback to cited chunks and maintain retrieval weights.

ALTER TABLE chunks
    ADD COLUMN IF NOT EXISTS positive_feedback_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS negative_feedback_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS positive_feedback_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS feedback_status VARCHAR(32) NOT NULL DEFAULT 'normal';

CREATE INDEX IF NOT EXISTS idx_chunks_feedback_status
    ON chunks(tenant_id, knowledge_base_id, feedback_status);
CREATE INDEX IF NOT EXISTS idx_chunks_feedback_rate
    ON chunks(tenant_id, knowledge_base_id, positive_feedback_rate);

CREATE TABLE IF NOT EXISTS message_chunk_references (
    id BIGSERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    request_id VARCHAR(36) NOT NULL DEFAULT '',
    chunk_tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (message_id, chunk_id)
);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_message
    ON message_chunk_references(tenant_id, message_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_chunk
    ON message_chunk_references(chunk_tenant_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_kb
    ON message_chunk_references(chunk_tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_session
    ON message_chunk_references(tenant_id, session_id);

CREATE TABLE IF NOT EXISTS message_feedbacks (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    actor_id VARCHAR(512) NOT NULL,
    rating SMALLINT NOT NULL CHECK (rating IN (-1, 1)),
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    reason_detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, message_id, actor_id)
);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_session
    ON message_feedbacks(tenant_id, session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_message
    ON message_feedbacks(tenant_id, message_id);

CREATE TABLE IF NOT EXISTS message_feedback_attributions (
    id BIGSERIAL PRIMARY KEY,
    feedback_id VARCHAR(36) NOT NULL REFERENCES message_feedbacks(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    rating SMALLINT NOT NULL CHECK (rating IN (-1, 1)),
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    reason_detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (feedback_id, chunk_id)
);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_chunk
    ON message_feedback_attributions(chunk_tenant_id, chunk_id, rating);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_kb
    ON message_feedback_attributions(chunk_tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_message
    ON message_feedback_attributions(tenant_id, message_id);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_reason
    ON message_feedback_attributions(chunk_tenant_id, reason_code)
    WHERE rating = -1 AND reason_code <> '';

CREATE TABLE IF NOT EXISTS chunk_feedback_weight_logs (
    id BIGSERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    knowledge_base_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    feedback_id VARCHAR(36) NOT NULL DEFAULT '',
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    trigger_source VARCHAR(32) NOT NULL,
    trigger_action VARCHAR(32) NOT NULL,
    old_positive_count BIGINT NOT NULL,
    new_positive_count BIGINT NOT NULL,
    old_negative_count BIGINT NOT NULL,
    new_negative_count BIGINT NOT NULL,
    old_positive_rate DOUBLE PRECISION NOT NULL,
    new_positive_rate DOUBLE PRECISION NOT NULL,
    old_recall_weight DOUBLE PRECISION NOT NULL,
    new_recall_weight DOUBLE PRECISION NOT NULL,
    old_status VARCHAR(32) NOT NULL,
    new_status VARCHAR(32) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_weight_logs_chunk
    ON chunk_feedback_weight_logs(tenant_id, knowledge_base_id, chunk_id, created_at DESC);

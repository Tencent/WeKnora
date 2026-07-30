-- Migration 000079: QA feedback (like/dislike) for assistant answers.

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1.0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS weight_updated_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS feedback JSONB;

CREATE TABLE IF NOT EXISTS message_chunk_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_mcl_message_id ON message_chunk_links (message_id);
CREATE INDEX IF NOT EXISTS idx_mcl_chunk_id ON message_chunk_links (chunk_id);
CREATE INDEX IF NOT EXISTS idx_mcl_tenant_id ON message_chunk_links (tenant_id);

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    old_weight DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    new_weight DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    reason VARCHAR(255) NOT NULL DEFAULT '',
    operator VARCHAR(64) NOT NULL DEFAULT 'system',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cwl_chunk_id ON chunk_weight_logs (chunk_id);
CREATE INDEX IF NOT EXISTS idx_cwl_tenant_id ON chunk_weight_logs (tenant_id);
CREATE INDEX IF NOT EXISTS idx_cwl_created_at ON chunk_weight_logs (created_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Adding chunk feedback fields and tables'; END $$;

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS positive_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS needs_optimization BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_chunks_kb_positive_rate ON chunks(knowledge_base_id, positive_rate);
CREATE INDEX IF NOT EXISTS idx_chunks_needs_optimization ON chunks(knowledge_base_id, needs_optimization);

CREATE TABLE IF NOT EXISTS message_chunk_refs (
    tenant_id INTEGER NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, message_id, chunk_id)
);

CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_message_id ON message_chunk_refs(message_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_kb_chunk ON message_chunk_refs(knowledge_base_id, chunk_id);

CREATE TABLE IF NOT EXISTS user_message_feedbacks (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    vote VARCHAR(10) NOT NULL,
    dislike_reason VARCHAR(100) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_message_feedbacks_unique ON user_message_feedbacks(tenant_id, user_id, message_id);
CREATE INDEX IF NOT EXISTS idx_user_message_feedbacks_session ON user_message_feedbacks(tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_user_message_feedbacks_message ON user_message_feedbacks(message_id);

CREATE TABLE IF NOT EXISTS chunk_recall_weight_logs (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    trigger_type VARCHAR(30) NOT NULL,
    user_id VARCHAR(36),
    message_id VARCHAR(36),
    old_weight DOUBLE PRECISION NOT NULL,
    new_weight DOUBLE PRECISION NOT NULL,
    like_count BIGINT NOT NULL,
    dislike_count BIGINT NOT NULL,
    positive_rate DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunk_recall_weight_logs_chunk_id ON chunk_recall_weight_logs(chunk_id, created_at DESC);

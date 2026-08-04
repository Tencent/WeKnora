-- Migration 000079: Q&A feedback accumulation onto knowledge chunks.
--
-- Adds per-chunk like/dislike counters, approval rate and recall weight, plus
-- the tables that link Q&A answers to the chunks they cited, record user
-- ratings, audit recall-weight changes and hold the tunable thresholds.

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS approval_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS needs_optimization BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS message_chunk_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    chunk_seq_id BIGINT NOT NULL DEFAULT 0,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_title VARCHAR(512) NOT NULL DEFAULT '',
    chunk_content TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_chunk_links_message_chunk ON message_chunk_links (message_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_links_chunk ON message_chunk_links (chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_links_tenant_chunk ON message_chunk_links (tenant_id, chunk_id);

CREATE TABLE IF NOT EXISTS chunk_feedback_records (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    rating VARCHAR(16) NOT NULL,
    reason VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chunk_feedback_records_user_message ON chunk_feedback_records (user_id, message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_records_message ON chunk_feedback_records (message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_records_tenant_created ON chunk_feedback_records (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    old_weight DOUBLE PRECISION NOT NULL DEFAULT 1,
    new_weight DOUBLE PRECISION NOT NULL DEFAULT 1,
    source VARCHAR(24) NOT NULL DEFAULT 'feedback',
    message_id VARCHAR(36) NOT NULL DEFAULT '',
    user_id VARCHAR(36) NOT NULL DEFAULT '',
    reason VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk ON chunk_weight_logs (chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_created ON chunk_weight_logs (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS chunk_feedback_configs (
    tenant_id BIGINT PRIMARY KEY,
    boost_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.8,
    degrade_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    optimize_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.4,
    min_votes BIGINT NOT NULL DEFAULT 1,
    weight_step DOUBLE PRECISION NOT NULL DEFAULT 0.1,
    max_weight DOUBLE PRECISION NOT NULL DEFAULT 2.0,
    min_weight DOUBLE PRECISION NOT NULL DEFAULT 0.1,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
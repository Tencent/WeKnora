-- Migration: 000075_chunk_feedback
-- Description: Persist message-to-chunk references and aggregate user feedback onto chunks.

DO $$ BEGIN RAISE NOTICE '[Migration 000075] Adding chunk feedback aggregate columns...'; END $$;

ALTER TABLE chunks
    ADD COLUMN IF NOT EXISTS like_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS dislike_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS positive_rate DECIMAL(5,4),
    ADD COLUMN IF NOT EXISTS recall_weight DECIMAL(4,2) NOT NULL DEFAULT 1.00,
    ADD COLUMN IF NOT EXISTS last_feedback_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS feedback_reset_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS needs_optimization BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_chunks_feedback_quality
    ON chunks(tenant_id, positive_rate, recall_weight);

CREATE INDEX IF NOT EXISTS idx_chunks_last_feedback_at
    ON chunks(last_feedback_at);

CREATE INDEX IF NOT EXISTS idx_chunks_feedback_reset_at
    ON chunks(feedback_reset_at);

CREATE INDEX IF NOT EXISTS idx_chunks_needs_optimization
    ON chunks(tenant_id, needs_optimization);

DO $$ BEGIN RAISE NOTICE '[Migration 000075] Creating message chunk reference table...'; END $$;

CREATE TABLE IF NOT EXISTS message_chunk_references (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    session_id        VARCHAR(36) NOT NULL,
    message_id        VARCHAR(36) NOT NULL,
    chunk_id          VARCHAR(36) NOT NULL,
    knowledge_id      VARCHAR(36),
    knowledge_base_id VARCHAR(36),
    created_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_msg_chunk_refs_unique
    ON message_chunk_references(tenant_id, message_id, chunk_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_msg_chunk_refs_message
    ON message_chunk_references(tenant_id, session_id, message_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_msg_chunk_refs_chunk
    ON message_chunk_references(tenant_id, chunk_id)
    WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000075] Creating message feedback table...'; END $$;

CREATE TABLE IF NOT EXISTS message_feedbacks (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    user_id    VARCHAR(512) NOT NULL DEFAULT '',
    action     VARCHAR(16) NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT chk_message_feedback_action CHECK (action IN ('like', 'dislike'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_feedbacks_unique
    ON message_feedbacks(tenant_id, message_id, user_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_message_feedbacks_message
    ON message_feedbacks(tenant_id, session_id, message_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_message_feedbacks_action
    ON message_feedbacks(tenant_id, action)
    WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000075] Creating chunk feedback weight log table...'; END $$;

CREATE TABLE IF NOT EXISTS chunk_feedback_weight_logs (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    chunk_id          VARCHAR(36) NOT NULL,
    knowledge_id      VARCHAR(36),
    knowledge_base_id VARCHAR(36),
    old_weight        DECIMAL(4,2) NOT NULL DEFAULT 1.00,
    new_weight        DECIMAL(4,2) NOT NULL DEFAULT 1.00,
    old_positive_rate DECIMAL(5,4),
    new_positive_rate DECIMAL(5,4),
    trigger_source    VARCHAR(32) NOT NULL,
    message_id        VARCHAR(36),
    created_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk
    ON chunk_feedback_weight_logs(tenant_id, chunk_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_source
    ON chunk_feedback_weight_logs(tenant_id, trigger_source, created_at DESC)
    WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000075] chunk feedback schema ready'; END $$;

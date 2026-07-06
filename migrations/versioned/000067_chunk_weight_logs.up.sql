-- Migration: 000067_chunk_weight_logs
-- Description: Audit trail for every recall_weight change on a chunk.
-- Records the old/new weight and approval rate, plus the trigger source
-- ('user_feedback' | 'admin_reset' | 'admin_manual') so admins can review
-- why a chunk's ranking position shifted.

DO $$ BEGIN RAISE NOTICE '[Migration 000067] Creating table: chunk_weight_logs'; END $$;

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id                  VARCHAR(36)   NOT NULL,
    tenant_id           BIGINT        NOT NULL,
    chunk_id            VARCHAR(36)   NOT NULL,
    old_weight          DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    new_weight          DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    old_approval_rate   DOUBLE PRECISION NOT NULL DEFAULT 0,
    new_approval_rate   DOUBLE PRECISION NOT NULL DEFAULT 0,
    old_like_count      INTEGER       NOT NULL DEFAULT 0,
    new_like_count      INTEGER       NOT NULL DEFAULT 0,
    old_dislike_count   INTEGER       NOT NULL DEFAULT 0,
    new_dislike_count   INTEGER       NOT NULL DEFAULT 0,
    trigger_type        VARCHAR(32)   NOT NULL,  -- 'user_feedback' | 'admin_reset' | 'admin_manual'
    trigger_detail      TEXT,                    -- human-readable note (e.g. dislike reason, admin id)
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_chunk
    ON chunk_weight_logs (tenant_id, chunk_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_type
    ON chunk_weight_logs (tenant_id, trigger_type, created_at DESC);

DO $$ BEGIN RAISE NOTICE '[Migration 000067] chunk_weight_logs table ready'; END $$;

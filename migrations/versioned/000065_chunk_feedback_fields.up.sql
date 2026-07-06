-- Migration: 000065_chunk_feedback_fields
-- Description: Add feedback/weight columns to the chunks table so that
-- user like/dislike actions can be aggregated per-chunk and a recall
-- weight can be derived from the approval rate.
--
-- Columns added:
--   like_count        – cumulative thumbs-up count attributed to this chunk
--   dislike_count     – cumulative thumbs-down count attributed to this chunk
--   approval_rate     – like_count / (like_count + dislike_count), 0.0–1.0
--   recall_weight     – multiplier applied during retrieval (default 1.0)
--   needs_optimization – flag set when approval_rate falls below threshold
--   feedback_updated_at – last time feedback counters were recomputed

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Adding feedback/weight columns to chunks...'; END $$;

ALTER TABLE chunks
    ADD COLUMN IF NOT EXISTS like_count          INTEGER      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS dislike_count       INTEGER      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS approval_rate       DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS recall_weight       DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    ADD COLUMN IF NOT EXISTS needs_optimization  BOOLEAN      NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS feedback_updated_at TIMESTAMP WITH TIME ZONE;

-- Index for "show me low-quality chunks" admin queries.
CREATE INDEX IF NOT EXISTS idx_chunks_approval_rate
    ON chunks (tenant_id, approval_rate);

-- Index for filtering chunks that need manual optimization.
CREATE INDEX IF NOT EXISTS idx_chunks_needs_optimization
    ON chunks (tenant_id, needs_optimization)
    WHERE needs_optimization = true;

DO $$ BEGIN RAISE NOTICE '[Migration 000065] chunk feedback columns ready'; END $$;

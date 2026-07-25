-- Migration: 000075_chunk_feedback (rollback)
-- Description: Drop message feedback tables and chunk aggregate columns.

DO $$ BEGIN RAISE NOTICE '[Migration 000075 rollback] Dropping message feedback schema...'; END $$;

DROP INDEX IF EXISTS idx_message_feedbacks_action;
DROP INDEX IF EXISTS idx_message_feedbacks_message;
DROP INDEX IF EXISTS idx_message_feedbacks_unique;
DROP TABLE IF EXISTS message_feedbacks;

DROP INDEX IF EXISTS idx_chunk_weight_logs_source;
DROP INDEX IF EXISTS idx_chunk_weight_logs_chunk;
DROP TABLE IF EXISTS chunk_feedback_weight_logs;

DROP INDEX IF EXISTS idx_msg_chunk_refs_chunk;
DROP INDEX IF EXISTS idx_msg_chunk_refs_message;
DROP INDEX IF EXISTS idx_msg_chunk_refs_unique;
DROP TABLE IF EXISTS message_chunk_references;

DROP INDEX IF EXISTS idx_chunks_needs_optimization;
DROP INDEX IF EXISTS idx_chunks_feedback_reset_at;
DROP INDEX IF EXISTS idx_chunks_last_feedback_at;
DROP INDEX IF EXISTS idx_chunks_feedback_quality;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS needs_optimization,
    DROP COLUMN IF EXISTS feedback_reset_at,
    DROP COLUMN IF EXISTS last_feedback_at,
    DROP COLUMN IF EXISTS recall_weight,
    DROP COLUMN IF EXISTS positive_rate,
    DROP COLUMN IF EXISTS dislike_count,
    DROP COLUMN IF EXISTS like_count;

DO $$ BEGIN RAISE NOTICE '[Migration 000075 rollback] chunk feedback schema dropped'; END $$;

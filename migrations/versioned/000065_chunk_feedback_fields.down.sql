-- Reverse migration for 000065_chunk_feedback_fields

DROP INDEX IF EXISTS idx_chunks_needs_optimization;
DROP INDEX IF EXISTS idx_chunks_approval_rate;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS feedback_updated_at,
    DROP COLUMN IF EXISTS needs_optimization,
    DROP COLUMN IF EXISTS recall_weight,
    DROP COLUMN IF EXISTS approval_rate,
    DROP COLUMN IF EXISTS dislike_count,
    DROP COLUMN IF EXISTS like_count;

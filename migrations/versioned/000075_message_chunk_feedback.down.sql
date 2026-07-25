DROP TABLE IF EXISTS chunk_feedback_weight_logs;
DROP TABLE IF EXISTS message_feedback_attributions;
DROP TABLE IF EXISTS message_feedbacks;
DROP TABLE IF EXISTS message_chunk_references;

DROP INDEX IF EXISTS idx_chunks_feedback_rate;
DROP INDEX IF EXISTS idx_chunks_feedback_status;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS feedback_status,
    DROP COLUMN IF EXISTS recall_weight,
    DROP COLUMN IF EXISTS positive_feedback_rate,
    DROP COLUMN IF EXISTS negative_feedback_count,
    DROP COLUMN IF EXISTS positive_feedback_count;

ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS feedback_reset_at;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS recall_weight,
    DROP COLUMN IF EXISTS positive_rate,
    DROP COLUMN IF EXISTS dislike_count,
    DROP COLUMN IF EXISTS like_count;

DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS message_feedbacks;
DROP TABLE IF EXISTS message_chunk_references;

DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS message_feedbacks;
DROP TABLE IF EXISTS message_chunk_refs;

ALTER TABLE chunks DROP COLUMN IF EXISTS feedback_reset_at;
ALTER TABLE chunks DROP COLUMN IF EXISTS needs_optimization;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS positive_rate;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS like_count;

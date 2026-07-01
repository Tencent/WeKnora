DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS message_feedbacks;
DROP TABLE IF EXISTS message_chunk_refs;

ALTER TABLE chunks DROP COLUMN feedback_reset_at;
ALTER TABLE chunks DROP COLUMN needs_optimization;
ALTER TABLE chunks DROP COLUMN recall_weight;
ALTER TABLE chunks DROP COLUMN positive_rate;
ALTER TABLE chunks DROP COLUMN dislike_count;
ALTER TABLE chunks DROP COLUMN like_count;

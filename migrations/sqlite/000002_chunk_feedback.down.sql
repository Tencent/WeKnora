DROP TABLE IF EXISTS chunk_feedback_configs;
DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS chunk_feedback_records;
DROP TABLE IF EXISTS message_chunk_links;
ALTER TABLE chunks DROP COLUMN like_count;
ALTER TABLE chunks DROP COLUMN dislike_count;
ALTER TABLE chunks DROP COLUMN approval_rate;
ALTER TABLE chunks DROP COLUMN recall_weight;
ALTER TABLE chunks DROP COLUMN needs_optimization;
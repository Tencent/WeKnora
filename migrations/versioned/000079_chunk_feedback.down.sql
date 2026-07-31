-- Roll back migration 000079.
DROP TABLE IF EXISTS chunk_feedback_configs;
DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS chunk_feedback_records;
DROP TABLE IF EXISTS message_chunk_links;
ALTER TABLE chunks DROP COLUMN IF EXISTS needs_optimization;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS approval_rate;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS like_count;
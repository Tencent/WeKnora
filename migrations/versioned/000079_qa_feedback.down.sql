-- Migration 000079 rollback: remove QA feedback columns and tables.

ALTER TABLE chunks DROP COLUMN IF EXISTS like_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS like_rate;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS weight_updated_at;

ALTER TABLE messages DROP COLUMN IF EXISTS feedback;

DROP TABLE IF EXISTS message_chunk_links;
DROP TABLE IF EXISTS chunk_weight_logs;

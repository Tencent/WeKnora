DO $$ BEGIN RAISE NOTICE '[Migration 000065] Dropping chunk feedback fields and tables'; END $$;

DROP TABLE IF EXISTS chunk_recall_weight_logs;
DROP TABLE IF EXISTS user_message_feedbacks;
DROP TABLE IF EXISTS message_chunk_refs;

DROP INDEX IF EXISTS idx_chunks_needs_optimization;
DROP INDEX IF EXISTS idx_chunks_kb_positive_rate;

ALTER TABLE chunks DROP COLUMN IF EXISTS needs_optimization;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS positive_rate;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS like_count;

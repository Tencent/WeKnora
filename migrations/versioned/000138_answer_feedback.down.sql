DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS chunk_feedback_attributions;
DROP TABLE IF EXISTS answer_feedbacks;
DROP TABLE IF EXISTS answer_chunk_links;

DROP INDEX IF EXISTS idx_chunks_kb_feedback;
ALTER TABLE chunks DROP COLUMN IF EXISTS needs_optimization;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS approval_rate;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS like_count;

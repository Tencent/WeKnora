DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS chunk_feedback_attributions;
DROP TABLE IF EXISTS answer_feedbacks;
DROP TABLE IF EXISTS answer_chunk_links;

DROP INDEX IF EXISTS idx_chunks_kb_feedback;
ALTER TABLE chunks DROP COLUMN needs_optimization;
ALTER TABLE chunks DROP COLUMN recall_weight;
ALTER TABLE chunks DROP COLUMN approval_rate;
ALTER TABLE chunks DROP COLUMN dislike_count;
ALTER TABLE chunks DROP COLUMN like_count;

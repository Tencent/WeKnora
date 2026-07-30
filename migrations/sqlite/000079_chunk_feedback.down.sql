DROP TABLE IF EXISTS chunk_weight_logs;
DROP TABLE IF EXISTS chunk_feedbacks;
DROP TABLE IF EXISTS qa_reply_chunk_ref_tombstones;
DROP TABLE IF EXISTS qa_reply_chunk_refs;

DROP INDEX IF EXISTS idx_messages_feedback;
DROP INDEX IF EXISTS idx_chunks_last_feedback_at;
DROP INDEX IF EXISTS idx_chunks_recall_weight;
DROP INDEX IF EXISTS idx_chunks_positive_rate;
DROP INDEX IF EXISTS idx_chunks_quality_status;

ALTER TABLE chunks DROP COLUMN last_feedback_at;
ALTER TABLE chunks DROP COLUMN dislike_reasons;
ALTER TABLE chunks DROP COLUMN quality_status;
ALTER TABLE chunks DROP COLUMN recall_weight;
ALTER TABLE chunks DROP COLUMN positive_rate;
ALTER TABLE chunks DROP COLUMN dislike_count;
ALTER TABLE chunks DROP COLUMN like_count;

ALTER TABLE messages DROP COLUMN dislike_count;
ALTER TABLE messages DROP COLUMN like_count;

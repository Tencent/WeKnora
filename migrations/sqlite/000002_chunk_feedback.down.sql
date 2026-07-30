DROP TABLE IF EXISTS chunk_feedback_audits;
DROP TABLE IF EXISTS message_feedbacks;
DROP TABLE IF EXISTS message_chunk_references;
ALTER TABLE chunks DROP COLUMN feedback_reset_at;
ALTER TABLE chunks DROP COLUMN recall_weight;
ALTER TABLE chunks DROP COLUMN positive_rate;
ALTER TABLE chunks DROP COLUMN dislike_count;
ALTER TABLE chunks DROP COLUMN like_count;

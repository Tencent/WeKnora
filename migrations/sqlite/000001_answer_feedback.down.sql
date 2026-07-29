ALTER TABLE knowledge_bases DROP COLUMN;
-- SQLite ALTER TABLE DROP COLUMN is supported in 3.35+. Fall back to a fresh
-- DB rebuild if running on older SQLite.

ALTER TABLE knowledge_bases DROP COLUMN feedback_reset_by;
ALTER TABLE knowledge_bases DROP COLUMN feedback_reset_at;

ALTER TABLE chunks DROP COLUMN needs_optimization;
ALTER TABLE chunks DROP COLUMN recall_weight;
ALTER TABLE chunks DROP COLUMN positive_rate;
ALTER TABLE chunks DROP COLUMN dislike_count;
ALTER TABLE chunks DROP COLUMN like_count;

DROP INDEX IF EXISTS idx_chunk_weight_logs_tenant_kb;
DROP INDEX IF EXISTS idx_chunk_weight_logs_kb_chunk;
DROP TABLE IF EXISTS chunk_weight_logs;

DROP INDEX IF EXISTS idx_message_feedbacks_message;
DROP INDEX IF EXISTS idx_message_feedbacks_tenant_session;
DROP INDEX IF EXISTS idx_message_feedbacks_message_user;
DROP TABLE IF EXISTS message_feedbacks;

DROP INDEX IF EXISTS idx_msg_chunk_refs_tenant_chunk;
DROP INDEX IF EXISTS idx_msg_chunk_refs_kb;
DROP INDEX IF EXISTS idx_msg_chunk_refs_chunk;
DROP INDEX IF EXISTS idx_msg_chunk_refs_message_chunk;
DROP TABLE IF EXISTS message_chunk_references;
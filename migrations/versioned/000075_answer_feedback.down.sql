ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS feedback_reset_by,
    DROP COLUMN IF EXISTS feedback_reset_at;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS recall_weight,
    DROP COLUMN IF EXISTS positive_rate,
    DROP COLUMN IF EXISTS dislike_count,
    DROP COLUMN IF EXISTS like_count;

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
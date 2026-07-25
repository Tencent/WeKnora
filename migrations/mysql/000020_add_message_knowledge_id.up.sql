-- MySQL 8 translation of 000020_add_message_knowledge_id.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN knowledge_id VARCHAR(36);
CREATE INDEX idx_messages_knowledge_id ON messages(knowledge_id);
ALTER TABLE tenants ADD COLUMN chat_history_config JSON;
ALTER TABLE tenants ADD COLUMN retrieval_config JSON;

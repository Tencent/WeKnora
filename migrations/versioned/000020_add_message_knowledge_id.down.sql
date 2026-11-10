-- Remove retrieval_config column from tenants table
ALTER TABLE tenants DROP COLUMN IF EXISTS retrieval_config;

-- Remove chat_history_config column from tenants table
ALTER TABLE tenants DROP COLUMN IF EXISTS chat_history_config;

-- Remove knowledge_id column from messages table
DROP INDEX IF EXISTS idx_messages_knowledge_id;
ALTER TABLE messages DROP COLUMN IF EXISTS knowledge_id;

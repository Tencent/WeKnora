-- Add knowledge_id column to messages table for linking messages to chat history knowledge base entries
ALTER TABLE messages ADD COLUMN IF NOT EXISTS knowledge_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_messages_knowledge_id ON messages(knowledge_id);

-- Add chat_history_config JSONB column to tenants table
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS chat_history_config JSONB;

-- Add retrieval_config JSONB column to tenants table
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS retrieval_config JSONB;

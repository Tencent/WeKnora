CREATE TABLE IF NOT EXISTS message_knowledge_chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) DEFAULT '',
    knowledge_base_id VARCHAR(36) DEFAULT '',
    chunk_index INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_knowledge_chunk ON message_knowledge_chunks(message_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_msg_knowledge_chunk_tenant ON message_knowledge_chunks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_msg_knowledge_chunk_session ON message_knowledge_chunks(session_id);
CREATE INDEX IF NOT EXISTS idx_msg_knowledge_chunk_chunk ON message_knowledge_chunks(chunk_id);
CREATE INDEX IF NOT EXISTS idx_msg_knowledge_chunk_knowledge ON message_knowledge_chunks(knowledge_id);
CREATE INDEX IF NOT EXISTS idx_msg_knowledge_chunk_kb ON message_knowledge_chunks(knowledge_base_id);

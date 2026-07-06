CREATE TABLE IF NOT EXISTS message_knowledge_chunk_relations (
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_chunk_relation ON message_knowledge_chunk_relations(message_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_rel_tenant ON message_knowledge_chunk_relations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_rel_session ON message_knowledge_chunk_relations(session_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_rel_chunk ON message_knowledge_chunk_relations(chunk_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_rel_knowledge ON message_knowledge_chunk_relations(knowledge_id);
CREATE INDEX IF NOT EXISTS idx_msg_chunk_rel_kb ON message_knowledge_chunk_relations(knowledge_base_id);

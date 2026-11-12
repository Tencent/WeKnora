CREATE TABLE IF NOT EXISTS knowledge_sub_knowledge (
    knowledge_id VARCHAR(36) PRIMARY KEY REFERENCES knowledges(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    source_namespace VARCHAR(32) NOT NULL,
    root_id VARCHAR(128) NOT NULL,
    node_type VARCHAR(16) NOT NULL CHECK (node_type IN ('root', 'child')),
    source_id VARCHAR(128) NOT NULL,
    reference_id VARCHAR(128) NOT NULL DEFAULT '',
    parent_id VARCHAR(128) NOT NULL DEFAULT '',
    sort_order BIGINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    source_hash VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME,
    CHECK (node_type != 'root' OR parent_id = ''),
    CHECK (source_id <> parent_id AND source_id <> reference_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_knowledge_active
    ON knowledge_sub_knowledge(tenant_id, knowledge_base_id, source_namespace, source_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_knowledge_root_active
    ON knowledge_sub_knowledge(tenant_id, knowledge_base_id, source_namespace, root_id)
    WHERE deleted_at IS NULL AND node_type = 'root';
CREATE INDEX IF NOT EXISTS idx_sub_knowledge_group
    ON knowledge_sub_knowledge(tenant_id, knowledge_base_id, source_namespace, root_id, node_type, sort_order, source_id)
    WHERE deleted_at IS NULL;

CREATE TRIGGER IF NOT EXISTS knowledge_sub_knowledge_scope
AFTER UPDATE OF tenant_id, knowledge_base_id, deleted_at ON knowledges
BEGIN
    UPDATE knowledge_sub_knowledge
    SET tenant_id = NEW.tenant_id,
        knowledge_base_id = NEW.knowledge_base_id,
        deleted_at = NEW.deleted_at,
        updated_at = CURRENT_TIMESTAMP
    WHERE knowledge_id = NEW.id;
END;

-- Keep deletion correct even for existing SQLite connections with FK checking
-- disabled. PostgreSQL always enforces the ON DELETE CASCADE foreign key.
CREATE TRIGGER IF NOT EXISTS knowledge_sub_knowledge_delete
AFTER DELETE ON knowledges
BEGIN
    DELETE FROM knowledge_sub_knowledge WHERE knowledge_id = OLD.id;
END;

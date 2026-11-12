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
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
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

-- Moves and soft deletion must atomically follow the authoritative Knowledge.
CREATE FUNCTION sync_knowledge_sub_knowledge_scope() RETURNS trigger AS $$
BEGIN
    UPDATE knowledge_sub_knowledge
    SET tenant_id = NEW.tenant_id,
        knowledge_base_id = NEW.knowledge_base_id,
        deleted_at = NEW.deleted_at,
        updated_at = CURRENT_TIMESTAMP
    WHERE knowledge_id = NEW.id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER knowledge_sub_knowledge_scope
    AFTER UPDATE OF tenant_id, knowledge_base_id, deleted_at ON knowledges
    FOR EACH ROW EXECUTE FUNCTION sync_knowledge_sub_knowledge_scope();

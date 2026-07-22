CREATE TABLE IF NOT EXISTS knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_scope_parent
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_path ON knowledge_folders (path);
CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_folders_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

ALTER TABLE knowledges
    ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_knowledges_folder_id ON knowledges (folder_id);

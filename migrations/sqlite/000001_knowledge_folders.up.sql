CREATE TABLE IF NOT EXISTS knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(100) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, knowledge_base_id, parent_id, name)
);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent ON knowledge_folders(tenant_id, knowledge_base_id, parent_id);
CREATE TABLE IF NOT EXISTS knowledge_folder_closure (
    ancestor_id VARCHAR(36) NOT NULL,
    descendant_id VARCHAR(36) NOT NULL,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    PRIMARY KEY (ancestor_id, descendant_id)
);
CREATE INDEX IF NOT EXISTS idx_kfc_ancestor ON knowledge_folder_closure(ancestor_id, depth, descendant_id);
CREATE INDEX IF NOT EXISTS idx_kfc_descendant ON knowledge_folder_closure(descendant_id, depth, ancestor_id);
ALTER TABLE knowledges ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_knowledges_folder ON knowledges(tenant_id, knowledge_base_id, folder_id);

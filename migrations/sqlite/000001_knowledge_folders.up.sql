CREATE TABLE IF NOT EXISTS knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_folder_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 1 CHECK (depth >= 1 AND depth <= 10),
    sort_order INTEGER NOT NULL DEFAULT 0,
    creator_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_kb_parent
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_folder_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at
    ON knowledge_folders (deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_folders_live_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, COALESCE(parent_folder_id, ''), LOWER(name))
    WHERE deleted_at IS NULL;

ALTER TABLE knowledges ADD COLUMN folder_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledges_kb_folder
    ON knowledges (tenant_id, knowledge_base_id, folder_id);

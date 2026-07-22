-- Migration: 000001_knowledge_folders

CREATE TABLE IF NOT EXISTS knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL CHECK (name <> ''),
    path              VARCHAR(2048) NOT NULL CHECK (path <> ''),
    depth             INTEGER NOT NULL CHECK (depth BETWEEN 1 AND 32),
    sort_order        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_folders_live_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent
    ON knowledge_folders (
        tenant_id,
        knowledge_base_id,
        parent_id,
        sort_order,
        name,
        created_at,
        id
    )
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_path
    ON knowledge_folders (tenant_id, knowledge_base_id, path)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at
    ON knowledge_folders (deleted_at);

ALTER TABLE knowledges ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE knowledges ADD COLUMN folder_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE knowledges ADD COLUMN folder_indexed_version INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_knowledges_folder
    ON knowledges (tenant_id, knowledge_base_id, folder_id, id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledges_folder_index_pending
    ON knowledges (tenant_id, knowledge_base_id, id)
    WHERE deleted_at IS NULL AND folder_indexed_version < folder_version;

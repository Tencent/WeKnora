-- Multi-level folder support for knowledge bases.
-- Folders are an adjacency list (parent_id, "" = root) with a materialized
-- "/"-joined name path for cheap display/sort, mirroring wiki_folders.

CREATE TABLE knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_knowledge_folders_kb ON knowledge_folders(knowledge_base_id);
CREATE INDEX idx_knowledge_folders_parent ON knowledge_folders(parent_id);
CREATE INDEX idx_knowledge_folders_path ON knowledge_folders(path);
CREATE UNIQUE INDEX uni_knowledge_folders_kb_parent_name
    ON knowledge_folders(knowledge_base_id, parent_id, name) WHERE deleted_at IS NULL;

-- Link a knowledge to the folder it lives in (empty = root level).
ALTER TABLE knowledges ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX idx_knowledges_folder ON knowledges(knowledge_base_id, folder_id);

CREATE TABLE IF NOT EXISTS knowledge_folders (
    id              TEXT    PRIMARY KEY,
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id TEXT    NOT NULL,
    parent_id       TEXT    NOT NULL DEFAULT '',
    name            TEXT    NOT NULL,
    path            TEXT    NOT NULL DEFAULT '',
    depth           INTEGER NOT NULL DEFAULT 0,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      DATETIME
);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_tenant_kb ON knowledge_folders (tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent ON knowledge_folders (parent_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at ON knowledge_folders (deleted_at);
ALTER TABLE knowledges ADD COLUMN folder_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_knowledges_folder_id ON knowledges (folder_id);

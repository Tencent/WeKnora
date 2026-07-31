-- 000080: Add multi-level knowledge folders support
CREATE TABLE IF NOT EXISTS knowledge_folders (
    id              VARCHAR(36)  PRIMARY KEY,
    tenant_id       INTEGER      NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id       VARCHAR(36)  NOT NULL DEFAULT '',
    name            VARCHAR(255) NOT NULL,
    path            VARCHAR(1024) NOT NULL DEFAULT '',
    depth           INTEGER      NOT NULL DEFAULT 0,
    sort_order      INTEGER      NOT NULL DEFAULT 0,
    created_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_tenant_kb ON knowledge_folders (tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent ON knowledge_folders (parent_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at ON knowledge_folders (deleted_at);
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_knowledges_folder_id ON knowledges (folder_id);

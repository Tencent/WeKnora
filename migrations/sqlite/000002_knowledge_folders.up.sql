-- SQLite counterpart of versioned/000079: multi-level folders for document
-- knowledge bases.
--
-- Kept as a separate migration rather than folded into 000000_init because
-- golang-migrate never re-runs an applied version: an existing SQLite database
-- would otherwise never receive the column, and SQLite has no
-- "ADD COLUMN IF NOT EXISTS" to make the init script idempotent.

CREATE TABLE IF NOT EXISTS knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INTEGER NOT NULL DEFAULT 0,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_folders_parent_name
    ON knowledge_folders (knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent
    ON knowledge_folders (knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_tenant_kb
    ON knowledge_folders (tenant_id, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_path
    ON knowledge_folders (knowledge_base_id, path);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at
    ON knowledge_folders (deleted_at);

-- '' = not filed anywhere (knowledge base root), so existing rows are already
-- in a valid state and no backfill is needed.
ALTER TABLE knowledges ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledges_kb_folder
    ON knowledges (knowledge_base_id, folder_id);

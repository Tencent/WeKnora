-- Migration: 000002_document_folders
-- Description: Multi-level folder support for document knowledge bases.
-- Existing documents remain at the virtual root through the empty-string
-- default. This migration is intentionally separate from 000000_init so
-- databases created by older releases are upgraded instead of silently
-- missing the new schema.

CREATE TABLE IF NOT EXISTS document_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_doc_folders_parent_name
    ON document_folders(tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_doc_folders_scope_parent
    ON document_folders(tenant_id, knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_doc_folders_deleted_at
    ON document_folders(deleted_at);

ALTER TABLE knowledges
    ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledges_folder
    ON knowledges(tenant_id, knowledge_base_id, folder_id);

-- The SQLite retriever metadata table was historically created lazily by
-- GORM rather than by 000000_init. Create its released pre-folder shape when
-- absent, then apply the same folder upgrade for both new and existing Lite
-- databases. Existing rows remain at the virtual root.
CREATE TABLE IF NOT EXISTS lite_embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    source_id TEXT NOT NULL,
    source_type INTEGER NOT NULL,
    chunk_id TEXT,
    knowledge_id TEXT,
    knowledge_base_id TEXT,
    tag_id TEXT,
    content TEXT NOT NULL,
    dimension INTEGER NOT NULL,
    is_enabled NUMERIC DEFAULT 1
);

ALTER TABLE lite_embeddings
    ADD COLUMN folder_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_lite_embeddings_folder_id
    ON lite_embeddings(folder_id);

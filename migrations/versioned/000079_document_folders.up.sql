-- Migration: 000079_document_folders
-- Description: Multi-level folder support for document knowledge bases.
-- Issue: https://github.com/Tencent/WeKnora/issues/1311
--
-- Adds the document_folders tree (adjacency list + materialized path) and a
-- scalar folder_id column on knowledges that files a document into a folder
-- (empty string = root). Mirrors the wiki_folders / wiki_pages.folder_id
-- precedent from migration 000061.

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Applying document_folders schema'; END $$;

-- ---------------------------------------------------------------------------
-- 1) document_folders table
-- Directory nodes for the document-KB browser. A folder exists independently
-- of any document, so empty folders persist. parent_id forms an adjacency-list
-- tree ('' = root); path is the materialized "/"-joined name chain kept for
-- cheap display/sort. knowledges.folder_id references id; renaming a folder
-- updates the cached paths in its subtree. The tree is bounded at the application
-- layer by MaxFolderDepth=20 / MaxFoldersPerKB=5000.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS document_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);

-- A folder name is unique among its live siblings under the same parent and
-- KB. tenant_id is included to keep multi-tenant deployments isolated.
CREATE UNIQUE INDEX IF NOT EXISTS idx_doc_folders_parent_name
    ON document_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_doc_folders_scope_parent
    ON document_folders (tenant_id, knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_doc_folders_deleted_at
    ON document_folders (deleted_at);

-- ---------------------------------------------------------------------------
-- 2) knowledges.folder_id
-- Files a document into a folder. Empty string = root (the default, so all
-- existing documents land at the root level). The composite index covers the
-- folder-scoped listing query (WHERE tenant_id=? AND knowledge_base_id=? AND
-- folder_id=?).
-- ---------------------------------------------------------------------------
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledges_folder
    ON knowledges (tenant_id, knowledge_base_id, folder_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] document_folders schema applied successfully'; END $$;

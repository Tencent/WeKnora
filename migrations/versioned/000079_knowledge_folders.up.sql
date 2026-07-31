-- Migration 000079: multi-level folders for document knowledge bases.
--
-- Folders are an organisational layer only: they change which documents a
-- listing or a retrieval scope selects, never how a document is parsed,
-- chunked or embedded. Nothing in the ingestion pipeline or the vector store
-- schema is touched, so this migration is safe to apply to a populated base.
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- 1) knowledge_folders table
--
-- parent_id forms an adjacency list ('' = knowledge base root) and path is a
-- materialized chain of *ids* ('/uuid-a/uuid-b/'), not names. Storing ids
-- rather than names is the deliberate difference from wiki_folders: a rename
-- then rewrites exactly one row instead of the whole subtree, and prefix
-- matching a subtree cannot be confused by a name that happens to be a prefix
-- of a sibling. depth is denormalized so the nesting cap can be enforced
-- without walking to the root.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    sort_order        INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);

-- A folder name is unique among its live siblings. Restricting the index to
-- deleted_at IS NULL lets a name be reused after its folder is removed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_folders_parent_name
    ON knowledge_folders (knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent
    ON knowledge_folders (knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_tenant_kb
    ON knowledge_folders (tenant_id, knowledge_base_id);

-- Subtree reads are "path LIKE '/a/b/%'", which this index serves as a range
-- scan on the leading literal.
CREATE INDEX IF NOT EXISTS idx_knowledge_folders_path
    ON knowledge_folders (knowledge_base_id, path);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at
    ON knowledge_folders (deleted_at);

-- ---------------------------------------------------------------------------
-- 2) knowledges.folder_id
--
-- '' means "not filed anywhere", i.e. the knowledge base root. A default of ''
-- rather than NULL keeps every existing document in a valid state without a
-- backfill and keeps the equality filter free of a NULL special case.
-- ---------------------------------------------------------------------------
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';

-- The listing query filters by knowledge base and folder together, so the
-- composite index matches the access pattern the feature introduces.
CREATE INDEX IF NOT EXISTS idx_knowledges_kb_folder
    ON knowledges (knowledge_base_id, folder_id);

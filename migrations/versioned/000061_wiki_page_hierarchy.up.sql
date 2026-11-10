-- Migration: 000061_wiki_page_hierarchy
-- Description: Add structured directory hierarchy fields to wiki_pages.

DO $$ BEGIN RAISE NOTICE '[Migration 000061] Applying wiki page hierarchy schema'; END $$;

ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS parent_slug VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS category_path JSONB DEFAULT '[]'::JSONB;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS wiki_path VARCHAR(1024) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS depth INT NOT NULL DEFAULT 0;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 0;

ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_wiki_pages_folder_id
    ON wiki_pages (folder_id);
-- ---------------------------------------------------------------------------
-- 1) wiki_folders table
-- First-class directory nodes for the wiki browser. A folder exists
-- independently of any page, so empty folders persist (the user can build a
-- skeleton and file pages into it later). parent_id forms an adjacency-list
-- tree ('' = root); path is the materialized "/"-joined name chain kept for
-- cheap display/sort. wiki_pages.folder_id references id; renaming/moving a
-- folder updates this row's subtree and the affected pages' cached path.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_folders (
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

-- A folder name is unique among its live siblings under the same parent.
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_folders_parent_name
    ON wiki_folders (knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_wiki_folders_parent
    ON wiki_folders (knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_wiki_folders_deleted_at
    ON wiki_folders (deleted_at);

UPDATE wiki_pages
SET
    category_path = COALESCE(category_path, '[]'::JSONB),
    depth = COALESCE(depth, 0),
    wiki_path = CASE
        WHEN COALESCE(wiki_path, '') <> '' THEN wiki_path
        WHEN page_type IN ('index', 'log') THEN page_type || '/' || COALESCE(NULLIF(title, ''), slug)
        ELSE page_type || '/' || COALESCE(NULLIF(title, ''), slug)
    END
WHERE wiki_path = '' OR wiki_path IS NULL OR category_path IS NULL OR depth IS NULL;

CREATE INDEX IF NOT EXISTS idx_wiki_pages_parent_slug
    ON wiki_pages (knowledge_base_id, parent_slug);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_tree
    ON wiki_pages (knowledge_base_id, page_type, wiki_path, sort_order, title);

DO $$ BEGIN RAISE NOTICE '[Migration 000061] wiki page hierarchy schema applied successfully'; END $$;

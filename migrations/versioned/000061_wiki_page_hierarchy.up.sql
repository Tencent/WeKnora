-- Migration: 000061_wiki_page_hierarchy
-- Description: Add structured directory hierarchy fields to wiki_pages.

DO $$ BEGIN RAISE NOTICE '[Migration 000061] Applying wiki page hierarchy schema'; END $$;

ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS parent_slug VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS category_path JSONB DEFAULT '[]'::JSONB;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS wiki_path VARCHAR(1024) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS depth INT NOT NULL DEFAULT 0;
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 0;

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

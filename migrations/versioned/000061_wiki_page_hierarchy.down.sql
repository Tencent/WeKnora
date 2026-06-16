-- Migration: 000061_wiki_page_hierarchy (rollback)

DROP INDEX IF EXISTS idx_wiki_pages_category_levels;
DROP INDEX IF EXISTS idx_wiki_pages_tree;
DROP INDEX IF EXISTS idx_wiki_pages_parent_slug;

ALTER TABLE wiki_pages DROP COLUMN IF EXISTS category_l3;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS category_l2;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS category_l1;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS sort_order;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS depth;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS wiki_path;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS category_path;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS parent_slug;

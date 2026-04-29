-- Migration: 000037_wiki_and_indexing (rollback)
-- Description: Reverse the wiki + indexing schema in the opposite order of the up migration.
DO $$ BEGIN RAISE NOTICE '[Migration 000037 DOWN] Reverting wiki + indexing schema'; END $$;

ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS indexing_strategy;

DROP TABLE IF EXISTS wiki_page_issues;

DROP TABLE IF EXISTS wiki_pages;

ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS wiki_config;

DO $$ BEGIN RAISE NOTICE '[Migration 000037 DOWN] wiki + indexing schema reverted successfully'; END $$;

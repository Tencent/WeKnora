-- Migration: 000079_wiki_provenance (rollback)
-- Existing wiki_pages.source_refs/chunk_refs were left untouched, so rolling
-- back removes only the additive provenance ledger.

DO $$ BEGIN RAISE NOTICE '[Migration 000079 rollback] Dropping Wiki provenance schema...'; END $$;

DROP TABLE IF EXISTS wiki_page_sources;
DROP TABLE IF EXISTS wiki_block_sources;
DROP TABLE IF EXISTS wiki_page_blocks;
DROP TABLE IF EXISTS wiki_provenance_page_revisions;
DROP TABLE IF EXISTS knowledge_revisions;

DO $$ BEGIN RAISE NOTICE '[Migration 000079 rollback] Wiki provenance schema removed'; END $$;

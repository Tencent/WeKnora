-- ============================================================================
-- Migration 000100: Drop chunks indexes that only slow down writes
-- ============================================================================
-- Every INSERT/UPDATE on chunks maintains all of its indexes. Three of them
-- never help a query:
--   * idx_chunks_chunk_type   — chunk_type has a handful of distinct values
--                               and is always combined with knowledge_id /
--                               knowledge_base_id, which already have indexes.
--   * idx_chunks_content_hash — no query filters on content_hash; FAQ diffing
--                               reads the column but scans by knowledge_base_id.
--   * idx_chunks_tenant_kg    — (tenant_id, knowledge_id) is covered by
--                               idx_chunks_knowledge_enabled, which leads with
--                               the high-cardinality knowledge_id.
-- ============================================================================

DO $$ BEGIN RAISE NOTICE '[Migration 000100] Dropping redundant chunks indexes'; END $$;

DROP INDEX IF EXISTS idx_chunks_chunk_type;
DROP INDEX IF EXISTS idx_chunks_content_hash;
DROP INDEX IF EXISTS idx_chunks_tenant_kg;

DO $$ BEGIN RAISE NOTICE '[Migration 000100] Redundant chunks indexes dropped'; END $$;

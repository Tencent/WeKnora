-- Migration: 000036_kb_vector_store_id (rollback)
-- Description: Remove vector_store_id column and its index from knowledge_bases.
--
-- WARNING: This rollback is destructive. It permanently drops the vector_store_id column
-- and all KB ↔ VectorStore bindings. To preserve bindings before rollback:
--
--   CREATE TABLE knowledge_bases_vsid_backup AS
--     SELECT id, tenant_id, vector_store_id FROM knowledge_bases
--     WHERE vector_store_id IS NOT NULL;
--
-- This down migration does NOT block execution when non-NULL rows exist, because blocking
-- during an emergency rollback can make an incident worse. The RAISE NOTICE below logs the
-- number of rows whose bindings will be lost so operators can reconstruct later if needed.
DO $$
DECLARE non_null_count BIGINT;
BEGIN
    SELECT COUNT(*) INTO non_null_count
    FROM knowledge_bases WHERE vector_store_id IS NOT NULL;
    IF non_null_count > 0 THEN
        RAISE NOTICE '[Migration 000036 DOWN] Dropping vector_store_id with % non-NULL rows; these bindings will be lost', non_null_count;
    ELSE
        RAISE NOTICE '[Migration 000036 DOWN] Removing vector_store_id column from knowledge_bases';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_knowledge_bases_tenant_vector_store;

ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS vector_store_id;

DO $$ BEGIN RAISE NOTICE '[Migration 000036 DOWN] vector_store_id column removed successfully'; END $$;

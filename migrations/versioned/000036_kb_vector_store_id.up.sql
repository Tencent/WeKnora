-- Migration: 000036_kb_vector_store_id
-- Description: Add vector_store_id column to knowledge_bases for per-KB VectorStore binding.
-- NULL means "use tenant's effective engines" (env store derived from RETRIEVE_DRIVER),
-- which preserves existing behavior for all pre-existing rows.
-- No foreign key: env store IDs are virtual (not present in vector_stores), and
-- VectorStore soft-delete must not cascade into KB references. Referential integrity is
-- enforced at the application layer.
-- On PostgreSQL 11+ this ALTER runs as metadata-only (O(1), no row rewrite) because the
-- column is nullable with no DEFAULT. PostgreSQL 10 and earlier are not supported.
DO $$ BEGIN RAISE NOTICE '[Migration 000036] Adding vector_store_id column to knowledge_bases'; END $$;

ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS vector_store_id VARCHAR(36);

COMMENT ON COLUMN knowledge_bases.vector_store_id IS
    'References vector_stores.id. NULL means tenant default (env store derived from RETRIEVE_DRIVER). '
    'Immutable after creation (enforced at ORM and service layer). No FK by design.';

-- Composite index: tenant_id first to align with the dominant query pattern. All KB queries
-- start with `WHERE tenant_id = $1`, so the composite index enforces tenant-scoped plans and
-- hardens against accidental cross-tenant access. A full (non-partial) index is used because
-- the target end-state has most KBs bound to a store, making a partial index short-lived.
-- Storage cost is negligible at typical KB row counts.
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_vector_store
    ON knowledge_bases(tenant_id, vector_store_id);

-- Note: CREATE INDEX here holds a SHARE lock briefly. For very large knowledge_bases tables
-- (hundreds of thousands of rows or more), operators may prefer CREATE INDEX CONCURRENTLY.
-- This requires running the statement outside a transaction, which the standard migration
-- runner does not support; execute the CONCURRENTLY variant from a separate operational
-- script in that case. Simply replacing the statement below with CONCURRENTLY inside this
-- file will cause the migration to fail and leave schema_migrations in a dirty state.

DO $$
DECLARE non_null_count BIGINT;
BEGIN
    SELECT COUNT(*) INTO non_null_count
    FROM knowledge_bases WHERE vector_store_id IS NOT NULL;
    RAISE NOTICE '[Migration 000036] knowledge_bases.vector_store_id added (% non-NULL rows)', non_null_count;
END $$;

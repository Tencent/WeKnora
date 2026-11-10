-- ============================================================================
-- Migration 000026: Add missing indexes for chunks query performance
-- ============================================================================
-- Covers grep_chunks tool queries that filter by (knowledge_base_id, tenant_id)
-- and aggregate by (knowledge_id) with is_enabled/deleted_at filters.
-- ============================================================================

DO $$ BEGIN RAISE NOTICE '[Migration 000026] Adding chunks query indexes'; END $$;

-- Index for main query: WHERE knowledge_base_id = ? AND tenant_id = ?
-- Also benefits JOIN and OR conditions across multiple KB-tenant pairs
CREATE INDEX IF NOT EXISTS idx_chunks_kb_tenant
    ON chunks(knowledge_base_id, tenant_id);

-- Index for count query: WHERE knowledge_id IN (...) AND is_enabled = true AND deleted_at IS NULL
-- GROUP BY knowledge_id
CREATE INDEX IF NOT EXISTS idx_chunks_knowledge_enabled
    ON chunks(knowledge_id, is_enabled, deleted_at);

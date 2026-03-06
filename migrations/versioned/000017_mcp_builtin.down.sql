-- ============================================================================
-- Migration 000017 DOWN: Remove is_builtin from mcp_services
-- ============================================================================

DO $$ BEGIN RAISE NOTICE '[Migration 000017 DOWN] Removing is_builtin column from mcp_services...'; END $$;

-- Drop index
DROP INDEX IF EXISTS idx_mcp_services_is_builtin;

-- Remove is_builtin column
ALTER TABLE mcp_services
DROP COLUMN IF EXISTS is_builtin;

DO $$ BEGIN RAISE NOTICE '[Migration 000017 DOWN] is_builtin column removed from mcp_services'; END $$;

-- ============================================================================
-- Migration 000017: Add is_builtin support for MCP services
-- ============================================================================

DO $$ BEGIN RAISE NOTICE '[Migration 000017] Adding is_builtin column to mcp_services...'; END $$;

-- Add is_builtin column to mcp_services
ALTER TABLE mcp_services ADD COLUMN IF NOT EXISTS is_builtin BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_mcp_services_is_builtin ON mcp_services(is_builtin);

DO $$ BEGIN RAISE NOTICE '[Migration 000017] is_builtin column added to mcp_services'; END $$;

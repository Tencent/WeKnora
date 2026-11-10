-- Migration: 000006_custom_agents (rollback)
-- Description: Remove custom agents table and related changes

DO $$ BEGIN RAISE NOTICE '[Migration 000006 DOWN] Starting custom agents rollback...'; END $$;

-- Remove agent_id column from sessions table
DO $$ BEGIN RAISE NOTICE '[Migration 000006 DOWN] Removing agent_id column from sessions table'; END $$;
DROP INDEX IF EXISTS idx_sessions_agent_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS agent_id;

-- Drop custom_agents table (includes built-in agents created during migration)
DO $$ BEGIN RAISE NOTICE '[Migration 000006 DOWN] Dropping table: custom_agents'; END $$;
DROP INDEX IF EXISTS idx_custom_agents_tenant_id;
DROP INDEX IF EXISTS idx_custom_agents_is_builtin;
DROP INDEX IF EXISTS idx_custom_agents_deleted_at;
DROP TABLE IF EXISTS custom_agents;

DO $$ BEGIN RAISE NOTICE '[Migration 000006 DOWN] Custom agents rollback completed!'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000042 DOWN] Dropping mcp_tool_approvals...'; END $$;

DROP INDEX IF EXISTS idx_mcp_tool_approvals_service_id;
DROP INDEX IF EXISTS idx_mcp_tool_approvals_tenant_svc_tool;
DROP TABLE IF EXISTS mcp_tool_approvals;

DO $$ BEGIN RAISE NOTICE '[Migration 000042 DOWN] Done'; END $$;

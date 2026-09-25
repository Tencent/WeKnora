DO $$ BEGIN RAISE NOTICE '[Migration 000074] Adding MCP OAuth refresh lease...'; END $$;

ALTER TABLE mcp_oauth_tokens
    ADD COLUMN IF NOT EXISTS refresh_lease_id VARCHAR(36),
    ADD COLUMN IF NOT EXISTS refresh_lease_until TIMESTAMP WITH TIME ZONE;

DO $$ BEGIN RAISE NOTICE '[Migration 000074] MCP OAuth refresh lease ready'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000118 down] Reverting MCP OAuth timestamps to timestamp...'; END $$;

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN expires_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING expires_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN created_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN updated_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING updated_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_clients
    ALTER COLUMN created_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_clients
    ALTER COLUMN updated_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING updated_at AT TIME ZONE 'UTC';

DO $$ BEGIN RAISE NOTICE '[Migration 000118 down] MCP OAuth timestamps reverted'; END $$;

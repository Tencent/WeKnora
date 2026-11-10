DO $$ BEGIN RAISE NOTICE '[Migration 000118] Converting MCP OAuth timestamps to timestamptz...'; END $$;

-- Naive TIMESTAMP values were read through a GORM session with TimeZone=UTC.
-- Treat existing literals as UTC when promoting to timestamptz. Rows written
-- by a process outside UTC keep their shifted value until the token is
-- authorized or refreshed again.
ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN expires_at TYPE TIMESTAMP WITH TIME ZONE
        USING expires_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_tokens
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE
        USING updated_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_clients
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_oauth_clients
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE
        USING updated_at AT TIME ZONE 'UTC';

DO $$ BEGIN RAISE NOTICE '[Migration 000118] MCP OAuth timestamps converted'; END $$;

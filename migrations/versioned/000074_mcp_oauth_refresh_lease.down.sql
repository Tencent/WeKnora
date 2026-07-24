ALTER TABLE mcp_oauth_tokens
    DROP COLUMN IF EXISTS refresh_lease_until,
    DROP COLUMN IF EXISTS refresh_lease_id;

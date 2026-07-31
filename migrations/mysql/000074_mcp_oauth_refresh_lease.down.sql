ALTER TABLE mcp_oauth_tokens
    DROP COLUMN refresh_lease_until,
    DROP COLUMN refresh_lease_id;

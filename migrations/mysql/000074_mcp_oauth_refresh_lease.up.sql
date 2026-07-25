ALTER TABLE mcp_oauth_tokens
    ADD COLUMN refresh_lease_id VARCHAR(36) NULL,
    ADD COLUMN refresh_lease_until DATETIME(3) NULL;

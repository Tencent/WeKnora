-- MCP OAuth2 support: per-service dynamically-registered clients and
-- per-user access/refresh tokens (issue: MCP OAuth2 authorization-code flow).
DO $$ BEGIN RAISE NOTICE '[Migration 000062] Creating mcp_oauth_clients / mcp_oauth_tokens...'; END $$;

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    client_id VARCHAR(512) NOT NULL,
    client_secret TEXT,
    redirect_uri VARCHAR(1024),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_clients_tenant_svc
    ON mcp_oauth_clients(tenant_id, service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32),
    expires_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_user_svc
    ON mcp_oauth_tokens(tenant_id, user_id, service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000062] mcp_oauth tables ready'; END $$;

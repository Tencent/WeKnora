-- MySQL 8 translation of 000062_mcp_oauth.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    client_id VARCHAR(512) NOT NULL,
    client_secret TEXT,
    redirect_uri VARCHAR(1024),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);
CREATE UNIQUE INDEX idx_mcp_oauth_clients_tenant_svc ON mcp_oauth_clients(tenant_id, service_id);
CREATE TABLE mcp_oauth_tokens (
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
CREATE INDEX idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);
CREATE INDEX idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);
CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens(tenant_id, user_id, service_id);

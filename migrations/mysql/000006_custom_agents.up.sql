-- MySQL 8 translation of 000006_custom_agents.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(36),
    config JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    PRIMARY KEY (id, tenant_id)
);
CREATE INDEX idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX idx_custom_agents_deleted_at ON custom_agents(deleted_at);
ALTER TABLE sessions ADD COLUMN agent_id VARCHAR(36);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);

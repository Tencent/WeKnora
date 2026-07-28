-- MySQL 8 translation of 000001_agent.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id INTEGER,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_deleted_at ON users(deleted_at);
ALTER TABLE users ADD COLUMN can_access_all_tenants TINYINT(1) NOT NULL DEFAULT 0;
CREATE TABLE auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token VARCHAR(512) NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    is_revoked TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX idx_auth_tokens_token ON auth_tokens(token);
CREATE INDEX idx_auth_tokens_token_type ON auth_tokens(token_type);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);
ALTER TABLE tenants ADD COLUMN context_config JSON;
ALTER TABLE tenants ADD COLUMN conversation_config JSON;
ALTER TABLE tenants ADD COLUMN web_search_config JSON;
ALTER TABLE knowledge_bases ADD COLUMN is_temporary TINYINT(1) NOT NULL DEFAULT 0;
ALTER TABLE knowledge_bases ADD COLUMN type VARCHAR(32) NOT NULL DEFAULT 'document';
ALTER TABLE knowledge_bases ADD COLUMN faq_config JSON;
ALTER TABLE knowledge_bases ADD COLUMN question_generation_config JSON NULL;
ALTER TABLE knowledge_bases DROP COLUMN rerank_model_id;
ALTER TABLE knowledges ADD COLUMN tag_id VARCHAR(36);
CREATE INDEX idx_knowledges_tag ON knowledges(tag_id);
ALTER TABLE knowledges ADD COLUMN summary_status VARCHAR(32) DEFAULT 'none';
CREATE INDEX idx_knowledges_summary_status ON knowledges(summary_status);
ALTER TABLE chunks ADD COLUMN metadata JSON;
ALTER TABLE chunks ADD COLUMN tag_id VARCHAR(36);
CREATE INDEX idx_chunks_tag ON chunks(tag_id);
ALTER TABLE chunks ADD COLUMN status INT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN content_hash VARCHAR(64);
CREATE INDEX idx_chunks_content_hash ON chunks(content_hash);
ALTER TABLE models ADD COLUMN is_builtin TINYINT(1) NOT NULL DEFAULT 0;
CREATE INDEX idx_models_is_builtin ON models(is_builtin);
CREATE TABLE knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);
CREATE INDEX idx_knowledge_tags_kb ON knowledge_tags(tenant_id, knowledge_base_id);
CREATE TABLE mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled TINYINT(1) DEFAULT 1,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512),
    headers JSON,
    auth_config JSON,
    advanced_config JSON,
    stdio_config JSON,
    env_vars JSON,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);
CREATE INDEX idx_mcp_services_tenant_id ON mcp_services(tenant_id);
CREATE INDEX idx_mcp_services_enabled ON mcp_services(enabled);
CREATE INDEX idx_mcp_services_deleted_at ON mcp_services(deleted_at);

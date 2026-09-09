-- Semantic modeling module (数据建模): SQLite variant
CREATE TABLE IF NOT EXISTS semantic_connections (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    config_encrypted TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    last_test_result TEXT,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_semantic_connections_tenant_name
    ON semantic_connections (tenant_id, name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS semantic_models (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    connection_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'cube',
    draft_yaml TEXT NOT NULL DEFAULT '',
    published_yaml TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    last_error TEXT NOT NULL DEFAULT '',
    allowed_groups TEXT NOT NULL DEFAULT '[]',
    member_visibility TEXT,
    version INTEGER NOT NULL DEFAULT 0,
    published_at DATETIME,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_semantic_models_tenant_name
    ON semantic_models (tenant_id, name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS semantic_model_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    model_id VARCHAR(36) NOT NULL,
    version INTEGER NOT NULL,
    yaml TEXT NOT NULL,
    allowed_groups TEXT NOT NULL DEFAULT '[]',
    member_visibility TEXT,
    note VARCHAR(255) NOT NULL DEFAULT '',
    published_by VARCHAR(64) NOT NULL DEFAULT '',
    published_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS semantic_data_groups (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_semantic_data_groups_tenant_name
    ON semantic_data_groups (tenant_id, name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS semantic_data_group_members (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    group_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS semantic_audit_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    target VARCHAR(128) NOT NULL DEFAULT '',
    detail TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

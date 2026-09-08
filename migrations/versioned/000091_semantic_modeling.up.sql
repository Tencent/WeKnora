-- Semantic modeling module (数据建模): database connections, Cube models,
-- data groups and module audit log. All tables are additive; upstream merges
-- never touch them. See internal/semantic/README.md.

-- Cube 数据库连接配置 (凭据 AES-256-GCM 加密后存 config_encrypted)
CREATE TABLE IF NOT EXISTS semantic_connections (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    config_encrypted TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    last_test_result JSONB,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_semantic_connections_tenant_name
    ON semantic_connections (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_semantic_connections_tenant
    ON semantic_connections (tenant_id);

-- Cube 语义模型 (YAML 为准: 草稿与已发布内容分开存)
CREATE TABLE IF NOT EXISTS semantic_models (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    connection_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'cube',
    draft_yaml TEXT NOT NULL DEFAULT '',
    published_yaml TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    last_error TEXT NOT NULL DEFAULT '',
    allowed_groups JSONB NOT NULL DEFAULT '[]',
    version INT NOT NULL DEFAULT 0,
    published_at TIMESTAMPTZ,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_semantic_models_tenant_name
    ON semantic_models (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_semantic_models_tenant
    ON semantic_models (tenant_id);
CREATE INDEX IF NOT EXISTS idx_semantic_models_connection
    ON semantic_models (connection_id);

-- 发布版本快照 (支持一键回滚)
CREATE TABLE IF NOT EXISTS semantic_model_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    model_id VARCHAR(36) NOT NULL,
    version INT NOT NULL,
    yaml TEXT NOT NULL,
    allowed_groups JSONB NOT NULL DEFAULT '[]',
    note VARCHAR(255) NOT NULL DEFAULT '',
    published_by VARCHAR(64) NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_semantic_model_versions_model
    ON semantic_model_versions (model_id, version);

-- 数据权限组 (模型 accessPolicy 的组来源)
CREATE TABLE IF NOT EXISTS semantic_data_groups (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_semantic_data_groups_tenant_name
    ON semantic_data_groups (tenant_id, name) WHERE deleted_at IS NULL;

-- 组成员 (用户 ↔ 数据组)
CREATE TABLE IF NOT EXISTS semantic_data_group_members (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    group_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_semantic_data_group_members_user
    ON semantic_data_group_members (tenant_id, user_id);

-- 模块操作审计 (建模/发布/连接变更可追溯)
CREATE TABLE IF NOT EXISTS semantic_audit_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    target VARCHAR(128) NOT NULL DEFAULT '',
    detail JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_semantic_audit_logs_tenant_time
    ON semantic_audit_logs (tenant_id, created_at DESC);

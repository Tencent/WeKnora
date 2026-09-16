-- Skill installation, snapshot, environment and catalog contracts (PostgreSQL 86–90).
CREATE TABLE tenant_skills (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    description TEXT,
    instructions TEXT,
    bundle_ref VARCHAR(1024),
    bundle_sha256 VARCHAR(64),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    installed_snapshot_id VARCHAR(255),
    status VARCHAR(32) NOT NULL,
    error TEXT,
    installing_since DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    install_session_id VARCHAR(36),
    install_message_id VARCHAR(36),
    envs TEXT CHECK (envs IS NULL OR json_valid(envs)),
    catalog_id VARCHAR(36)
);
CREATE UNIQUE INDEX uq_tenant_skills_config_name
    ON tenant_skills(sandbox_config_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_tenant_skills_catalog ON tenant_skills(catalog_id);

CREATE TABLE tenant_skill_snapshots (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    skill_id VARCHAR(36),
    snapshot_id VARCHAR(255),
    parent_snapshot_id VARCHAR(255),
    generation INTEGER NOT NULL DEFAULT 0,
    "trigger" VARCHAR(16) NOT NULL,
    state VARCHAR(16) NOT NULL,
    superseded_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    planned_name VARCHAR(255)
);
CREATE INDEX idx_tenant_skill_snapshots_config ON tenant_skill_snapshots(sandbox_config_id);
CREATE INDEX idx_tenant_skill_snapshots_state ON tenant_skill_snapshots(state);

CREATE TABLE tenant_user_env_vars (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    principal_type VARCHAR(32) NOT NULL,
    principal_id VARCHAR(512) NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    skill_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    value TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_user_env_var ON tenant_user_env_vars
    (tenant_id, principal_type, principal_id, sandbox_config_id, skill_id, name);
CREATE INDEX idx_user_env_var_skill ON tenant_user_env_vars(tenant_id, skill_id);
CREATE INDEX idx_user_env_var_config ON tenant_user_env_vars(tenant_id, sandbox_config_id);

CREATE TABLE tenant_skill_catalog (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    description TEXT,
    instructions TEXT,
    bundle_ref VARCHAR(1024),
    bundle_sha256 VARCHAR(64),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX uq_tenant_skill_catalog_name
    ON tenant_skill_catalog(tenant_id, name) WHERE deleted_at IS NULL;

-- Mirrors versioned 000086–000090 and 000104:
-- tenant_skills, snapshot ledger, catalog, and per-principal env vars.
-- SQLite never received these tables; Lite shell_exec then failed with
-- "no such table: tenant_user_env_vars" when resolving skill credentials.
-- New tables are created in their current shape, so ALTER steps are skipped.
-- Catalog backfill from existing installs is omitted: this table set is new
-- on SQLite, so there are no tenant_skills rows to collapse.

CREATE TABLE IF NOT EXISTS tenant_skills (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    catalog_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    description TEXT,
    instructions TEXT,
    bundle_ref VARCHAR(1024),
    bundle_sha256 VARCHAR(64),
    enabled INTEGER NOT NULL DEFAULT 1,
    installed_snapshot_id VARCHAR(255),
    install_session_id VARCHAR(36),
    install_message_id VARCHAR(36),
    envs TEXT,
    served TEXT,
    status VARCHAR(32) NOT NULL,
    error TEXT,
    installing_since DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skills_config_name
    ON tenant_skills (sandbox_config_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tenant_skills_catalog
    ON tenant_skills (catalog_id);

CREATE TABLE IF NOT EXISTS tenant_skill_snapshots (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    skill_id VARCHAR(36),
    snapshot_id VARCHAR(255),
    parent_snapshot_id VARCHAR(255),
    generation INTEGER NOT NULL DEFAULT 0,
    planned_name VARCHAR(255),
    trigger VARCHAR(16) NOT NULL,
    state VARCHAR(16) NOT NULL,
    superseded_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tenant_skill_snapshots_config
    ON tenant_skill_snapshots (sandbox_config_id);
CREATE INDEX IF NOT EXISTS idx_tenant_skill_snapshots_state
    ON tenant_skill_snapshots (state);

CREATE TABLE IF NOT EXISTS tenant_skill_catalog (
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

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skill_catalog_name
    ON tenant_skill_catalog (tenant_id, name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS tenant_user_env_vars (
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

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_env_var
    ON tenant_user_env_vars (tenant_id, principal_type, principal_id, sandbox_config_id, skill_id, name);
CREATE INDEX IF NOT EXISTS idx_user_env_var_skill
    ON tenant_user_env_vars (tenant_id, skill_id);
CREATE INDEX IF NOT EXISTS idx_user_env_var_config
    ON tenant_user_env_vars (tenant_id, sandbox_config_id);

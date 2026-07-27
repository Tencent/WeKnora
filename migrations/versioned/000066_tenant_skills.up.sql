DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating tenant skills...'; END $$;

CREATE TABLE tenant_skills (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(50) NOT NULL,
    description VARCHAR(500) NOT NULL,
    category VARCHAR(32) NOT NULL DEFAULT 'other',
    status VARCHAR(16) NOT NULL DEFAULT 'enabled',
    current_version_id VARCHAR(36),
    has_scripts BOOLEAN NOT NULL DEFAULT FALSE,
    uploaded_by VARCHAR(36) NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT chk_tenant_skills_status CHECK (status IN ('enabled', 'disabled')),
    CONSTRAINT chk_tenant_skills_category CHECK (category IN ('content', 'data', 'development', 'workflow', 'other'))
);

CREATE UNIQUE INDEX uq_tenant_skills_live_name
    ON tenant_skills (tenant_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX idx_tenant_skills_tenant_status ON tenant_skills (tenant_id, status) WHERE deleted_at IS NULL;

CREATE TABLE tenant_skill_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    skill_id VARCHAR(36) NOT NULL REFERENCES tenant_skills(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    state VARCHAR(16) NOT NULL,
    storage_path VARCHAR(512) NOT NULL,
    content_hash CHAR(64) NOT NULL,
    manifest_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by VARCHAR(36) NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    garbage_at TIMESTAMPTZ,
    CONSTRAINT chk_tenant_skill_versions_state CHECK (state IN ('staging', 'ready', 'current', 'garbage')),
    CONSTRAINT uq_tenant_skill_versions_number UNIQUE (skill_id, version)
);

CREATE UNIQUE INDEX uq_tenant_skill_versions_current
    ON tenant_skill_versions (skill_id) WHERE state = 'current';
CREATE INDEX idx_tenant_skill_versions_reconcile ON tenant_skill_versions (state, created_at);

ALTER TABLE tenant_skills
    ADD CONSTRAINT fk_tenant_skills_current_version
    FOREIGN KEY (current_version_id) REFERENCES tenant_skill_versions(id);

CREATE TABLE skill_execution_audits (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    skill_id VARCHAR(36) NOT NULL REFERENCES tenant_skills(id) ON DELETE CASCADE,
    version_id VARCHAR(36) NOT NULL REFERENCES tenant_skill_versions(id),
    user_id VARCHAR(36) NOT NULL REFERENCES users(id),
    script_path VARCHAR(512) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'running',
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    exit_code INTEGER,
    killed BOOLEAN NOT NULL DEFAULT FALSE,
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    output_summary VARCHAR(4096) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_skill_execution_audits_tenant_started
    ON skill_execution_audits (tenant_id, started_at DESC);
CREATE INDEX idx_skill_execution_audits_running
    ON skill_execution_audits (status, started_at) WHERE status = 'running';

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Tenant skills ready'; END $$;

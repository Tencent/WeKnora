DO $$ BEGIN RAISE NOTICE '[Migration 000067] Creating agent collection profiles...'; END $$;

CREATE TABLE agent_collection_profiles (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    schema_version BIGINT NOT NULL DEFAULT 0,
    values JSONB NOT NULL DEFAULT '{}'::jsonb,
    inactive_values JSONB NOT NULL DEFAULT '{}'::jsonb,
    required_total INTEGER NOT NULL DEFAULT 0,
    completed_required INTEGER NOT NULL DEFAULT 0,
    is_complete BOOLEAN NOT NULL DEFAULT FALSE,
    lock_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX uq_agent_collection_profiles_live_scope
    ON agent_collection_profiles (tenant_id, agent_id, user_id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_collection_profiles_admin_list
    ON agent_collection_profiles (updated_at DESC, tenant_id, agent_id, user_id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_collection_profiles_agent ON agent_collection_profiles (agent_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_collection_profiles_agent_tenant ON agent_collection_profiles (agent_tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_collection_profiles_user ON agent_collection_profiles (user_id) WHERE deleted_at IS NULL;

CREATE TABLE agent_collection_history (
    id VARCHAR(36) PRIMARY KEY,
    profile_id VARCHAR(36) NOT NULL REFERENCES agent_collection_profiles(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    field_key VARCHAR(64) NOT NULL,
    schema_version BIGINT NOT NULL,
    old_value JSONB,
    new_value JSONB,
    source VARCHAR(32) NOT NULL,
    confidence NUMERIC(5,4),
    source_message_id VARCHAR(36),
    source_message_at TIMESTAMPTZ,
    actor_user_id VARCHAR(36),
    change_reason VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_agent_collection_history_source
        CHECK (source IN ('structured_answer', 'message_extraction', 'system_admin', 'schema_migration'))
);

CREATE INDEX idx_agent_collection_history_profile_created
    ON agent_collection_history (profile_id, created_at DESC);
CREATE INDEX idx_agent_collection_history_tenant_agent
    ON agent_collection_history (tenant_id, agent_id, created_at DESC);
CREATE INDEX idx_agent_collection_history_user ON agent_collection_history (user_id, created_at DESC);
CREATE INDEX idx_agent_collection_history_field ON agent_collection_history (field_key, created_at DESC);

CREATE TABLE agent_collection_exports (
    id VARCHAR(36) PRIMARY KEY,
    actor_user_id VARCHAR(36) NOT NULL,
    format VARCHAR(8) NOT NULL,
    filter_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    storage_path VARCHAR(512),
    filename VARCHAR(255),
    row_count BIGINT NOT NULL DEFAULT 0,
    error_message VARCHAR(1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ,
    CONSTRAINT chk_agent_collection_exports_format CHECK (format IN ('csv', 'xlsx')),
    CONSTRAINT chk_agent_collection_exports_status CHECK (status IN ('pending', 'running', 'completed', 'failed'))
);

CREATE INDEX idx_agent_collection_exports_actor_created
    ON agent_collection_exports (actor_user_id, created_at DESC);
CREATE INDEX idx_agent_collection_exports_expiry
    ON agent_collection_exports (expires_at) WHERE status = 'completed';

DO $$ BEGIN RAISE NOTICE '[Migration 000067] Agent collection profiles ready'; END $$;

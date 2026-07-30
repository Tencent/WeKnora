CREATE TABLE IF NOT EXISTS derived_artifacts (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    artifact_key VARCHAR(64) NOT NULL,
    artifact_kind VARCHAR(64) NOT NULL,
    input_digest VARCHAR(64) NOT NULL,
    model_id VARCHAR(255) NOT NULL DEFAULT '',
    model_revision VARCHAR(128) NOT NULL DEFAULT '',
    prompt_version VARCHAR(128) NOT NULL DEFAULT '',
    config_digest VARCHAR(64) NOT NULL DEFAULT '',
    producer_version VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    payload BYTEA,
    payload_encoding VARCHAR(32) NOT NULL DEFAULT '',
    object_uri TEXT,
    payload_digest VARCHAR(64) NOT NULL DEFAULT '',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    error_message VARCHAR(2048) NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    owner_token VARCHAR(128) NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMPTZ,
    CONSTRAINT uk_derived_artifacts_tenant_key UNIQUE (tenant_id, artifact_key),
    CONSTRAINT chk_derived_artifacts_status CHECK (status IN ('pending','computing','succeeded','failed'))
);
CREATE INDEX IF NOT EXISTS idx_derived_artifacts_kind_status ON derived_artifacts(artifact_kind, status);
CREATE INDEX IF NOT EXISTS idx_derived_artifacts_lease ON derived_artifacts(status, lease_expires_at);

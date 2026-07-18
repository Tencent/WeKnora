-- Migration: 000071_processing_artifact_cache
-- Durable, tenant-scoped content-addressed cache for ingestion artifacts.

CREATE TABLE IF NOT EXISTS processing_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind VARCHAR(32) NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    input_hash VARCHAR(64) NOT NULL DEFAULT '',
    model_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    prompt_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    config_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    schema_version VARCHAR(32) NOT NULL DEFAULT 'v1',
    status VARCHAR(16) NOT NULL,
    result_json JSONB,
    result_size BIGINT NOT NULL DEFAULT 0,
    error_detail TEXT NOT NULL DEFAULT '',
    lease_owner VARCHAR(64) NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMP WITH TIME ZONE,
    hit_count BIGINT NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_processing_artifacts_cache_key
    ON processing_artifacts(tenant_id, kind, cache_key);
CREATE INDEX IF NOT EXISTS idx_processing_artifacts_lease
    ON processing_artifacts(status, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_processing_artifacts_accessed
    ON processing_artifacts(last_accessed_at);

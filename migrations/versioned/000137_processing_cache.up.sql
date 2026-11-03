CREATE TABLE processing_cache (
    tenant_id BIGINT NOT NULL,
    scope_id VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    stage VARCHAR(64) NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    payload BYTEA NOT NULL,
    checksum CHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, scope_id, stage, cache_key)
);
CREATE INDEX idx_processing_cache_expires_at ON processing_cache(expires_at);
CREATE INDEX idx_processing_cache_scope_id ON processing_cache(scope_id);
CREATE INDEX idx_processing_cache_shared ON processing_cache(tenant_id, stage, cache_key);

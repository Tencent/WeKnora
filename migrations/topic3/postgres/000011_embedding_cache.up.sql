CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    tenant_id BIGINT NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    model_fingerprint CHAR(64) NOT NULL,
    request_options_sha256 CHAR(64) NOT NULL,
    text_sha256 CHAR(64) NOT NULL,
    embedding BYTEA NOT NULL,
    dimension INTEGER NOT NULL,
    checksum_sha256 CHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accessed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT embedding_cache_entries_pkey PRIMARY KEY (
        tenant_id, model_id, model_fingerprint, request_options_sha256, text_sha256
    ),
    CONSTRAINT embedding_cache_entries_dimension_check CHECK (dimension > 0),
    CONSTRAINT embedding_cache_entries_embedding_check CHECK (octet_length(embedding) = dimension * 4)
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_entries_expires
    ON embedding_cache_entries (expires_at, tenant_id, model_id);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_entries_accessed
    ON embedding_cache_entries (accessed_at, tenant_id, model_id);

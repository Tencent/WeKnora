DO $$ BEGIN RAISE NOTICE '[Migration 000072] Creating artifact cache table...'; END $$;

CREATE TABLE IF NOT EXISTS artifact_caches (
    id              VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id       BIGINT      NOT NULL,
    cache_key       VARCHAR(128) NOT NULL,
    cache_type      VARCHAR(32)  NOT NULL,
    input_hash      VARCHAR(64)  NOT NULL,
    config_hash     VARCHAR(64)  NOT NULL DEFAULT '',
    output_json     JSONB,
    output_text     TEXT,
    output_size     BIGINT       NOT NULL DEFAULT 0,
    computed_at     TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    expires_at      TIMESTAMP    NULL,
    created_at      TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMP    NULL,

    CONSTRAINT uq_artifact_cache_compound UNIQUE (tenant_id, cache_key, cache_type, input_hash, config_hash)
);

CREATE INDEX IF NOT EXISTS idx_artifact_caches_tenant_key
    ON artifact_caches(tenant_id, cache_key);

CREATE INDEX IF NOT EXISTS idx_artifact_caches_type_input
    ON artifact_caches(cache_type, input_hash);

CREATE INDEX IF NOT EXISTS idx_artifact_caches_expires
    ON artifact_caches(expires_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000072] Artifact cache table created'; END $$;

-- SQLite migration for artifact cache entries used by Lite deployments.

CREATE TABLE IF NOT EXISTS artifact_caches (
    id          TEXT NOT NULL PRIMARY KEY,
    tenant_id   INTEGER NOT NULL,
    cache_key   TEXT NOT NULL,
    cache_type  TEXT NOT NULL,
    input_hash  TEXT NOT NULL,
    config_hash TEXT NOT NULL DEFAULT '',
    output_json TEXT,
    output_text TEXT,
    output_size INTEGER NOT NULL DEFAULT 0,
    computed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at  DATETIME NULL,

    UNIQUE (tenant_id, cache_key, cache_type, input_hash, config_hash)
);

CREATE INDEX IF NOT EXISTS idx_artifact_caches_tenant_key
    ON artifact_caches(tenant_id, cache_key);

CREATE INDEX IF NOT EXISTS idx_artifact_caches_type_input
    ON artifact_caches(cache_type, input_hash);

CREATE INDEX IF NOT EXISTS idx_artifact_caches_expires
    ON artifact_caches(expires_at);

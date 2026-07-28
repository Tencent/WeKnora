-- Migration: 000002_content_addressed_rebuild_cache

CREATE TABLE IF NOT EXISTS embedding_caches (
    id          TEXT PRIMARY KEY,
    tenant_id   INTEGER NOT NULL,
    model_id    TEXT NOT NULL,
    dimension   INTEGER NOT NULL,
    input_hash  TEXT NOT NULL,
    vector      TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_embedding_cache_key
    ON embedding_caches (tenant_id, model_id, dimension, input_hash);
CREATE INDEX IF NOT EXISTS idx_embedding_caches_tenant_id
    ON embedding_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_embedding_caches_input_hash
    ON embedding_caches (input_hash);

CREATE TABLE IF NOT EXISTS generation_caches (
    id             TEXT PRIMARY KEY,
    tenant_id      INTEGER NOT NULL,
    namespace      TEXT NOT NULL,
    scope_id       TEXT NOT NULL DEFAULT '',
    model_id       TEXT NOT NULL,
    input_hash     TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    prompt_hash    TEXT NOT NULL,
    output         TEXT NOT NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_generation_cache_key
    ON generation_caches (tenant_id, namespace, scope_id, model_id, input_hash, prompt_version, prompt_hash);
CREATE INDEX IF NOT EXISTS idx_generation_caches_tenant_namespace
    ON generation_caches (tenant_id, namespace);
CREATE INDEX IF NOT EXISTS idx_generation_caches_scope_id
    ON generation_caches (scope_id);
CREATE INDEX IF NOT EXISTS idx_generation_caches_input_hash
    ON generation_caches (input_hash);

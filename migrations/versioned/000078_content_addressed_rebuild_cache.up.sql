-- Migration: 000078_content_addressed_rebuild_cache
-- Description: Add content-addressed caches for rebuild reuse.

DO $$ BEGIN RAISE NOTICE '[Migration 000078] Applying content-addressed rebuild cache schema'; END $$;

CREATE TABLE IF NOT EXISTS embedding_caches (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    model_id    VARCHAR(128) NOT NULL,
    dimension   INT NOT NULL,
    input_hash  VARCHAR(64) NOT NULL,
    vector      JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_embedding_cache_key
    ON embedding_caches (tenant_id, model_id, dimension, input_hash);
CREATE INDEX IF NOT EXISTS idx_embedding_caches_tenant_id
    ON embedding_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_embedding_caches_input_hash
    ON embedding_caches (input_hash);

CREATE TABLE IF NOT EXISTS generation_caches (
    id             VARCHAR(36) PRIMARY KEY,
    tenant_id      BIGINT NOT NULL,
    namespace      VARCHAR(64) NOT NULL,
    scope_id       VARCHAR(128) NOT NULL DEFAULT '',
    model_id       VARCHAR(128) NOT NULL,
    input_hash     VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(32) NOT NULL,
    prompt_hash    VARCHAR(64) NOT NULL,
    output         JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_generation_cache_key
    ON generation_caches (tenant_id, namespace, scope_id, model_id, input_hash, prompt_version, prompt_hash);
CREATE INDEX IF NOT EXISTS idx_generation_caches_tenant_namespace
    ON generation_caches (tenant_id, namespace);
CREATE INDEX IF NOT EXISTS idx_generation_caches_scope_id
    ON generation_caches (scope_id);
CREATE INDEX IF NOT EXISTS idx_generation_caches_input_hash
    ON generation_caches (input_hash);

DO $$ BEGIN RAISE NOTICE '[Migration 000078] content-addressed rebuild cache schema applied'; END $$;

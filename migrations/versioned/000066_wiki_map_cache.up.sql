-- Migration: 000066_wiki_map_cache
--
-- Cache deterministic LLM outputs from the per-document Wiki map phase.
-- Wiki reduce/merge is intentionally uncached because it depends on current
-- knowledge-base page state.

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating table: wiki_map_caches'; END $$;

CREATE TABLE IF NOT EXISTS wiki_map_caches (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    cache_key   VARCHAR(64) NOT NULL,
    kind        VARCHAR(64) NOT NULL,
    content_key VARCHAR(64) NOT NULL,
    model_id    VARCHAR(128) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    schema_ver  VARCHAR(32) NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wiki_map_caches_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_kind_content
    ON wiki_map_caches (kind, content_key);

CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_tenant_kind_model
    ON wiki_map_caches (tenant_id, kind, model_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] wiki_map_caches table ready'; END $$;

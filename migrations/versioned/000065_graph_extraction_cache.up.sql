-- Migration: 000065_graph_extraction_cache
--
-- Cache deterministic GraphRAG per-chunk extraction results. The cached value
-- is the LLM-produced graph JSON only; graph storage is still rebuilt into the
-- current knowledge namespace on every parse/reparse.

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating table: graph_extraction_caches'; END $$;

CREATE TABLE IF NOT EXISTS graph_extraction_caches (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    cache_key   VARCHAR(64) NOT NULL,
    content_key VARCHAR(64) NOT NULL,
    model_id    VARCHAR(128) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    schema_ver  VARCHAR(32) NOT NULL,
    graph       JSONB NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_graph_extraction_caches_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_content_key
    ON graph_extraction_caches (content_key);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_tenant_model
    ON graph_extraction_caches (tenant_id, model_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] graph_extraction_caches table ready'; END $$;

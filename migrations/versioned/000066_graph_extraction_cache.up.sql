-- Migration: 000066_graph_extraction_cache
--
-- Cache deterministic per-chunk GraphRAG extraction results. Cached graphs are
-- rebound to the current chunk ID before being written to graph storage.

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating table: graph_extraction_caches'; END $$;

CREATE TABLE IF NOT EXISTS graph_extraction_caches (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    cache_key    VARCHAR(64) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    model_id     VARCHAR(128) NOT NULL DEFAULT '',
    config_hash  VARCHAR(64) NOT NULL,
    schema_ver   VARCHAR(32) NOT NULL,
    graph        JSONB NOT NULL,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_graph_extraction_caches_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_content_hash
    ON graph_extraction_caches (content_hash);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_tenant_model
    ON graph_extraction_caches (tenant_id, model_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] graph_extraction_caches table ready'; END $$;

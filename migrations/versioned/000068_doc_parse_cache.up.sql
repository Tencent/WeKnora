-- Migration: 000068_doc_parse_cache
--
-- Cache deterministic DocReader outputs for file-backed knowledge. URL pages
-- are excluded at service level because their content may change without the
-- URL changing.

DO $$ BEGIN RAISE NOTICE '[Migration 000068] Creating table: doc_parse_caches'; END $$;

CREATE TABLE IF NOT EXISTS doc_parse_caches (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    cache_key    VARCHAR(64) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    parser       VARCHAR(128) NOT NULL DEFAULT '',
    config_hash  VARCHAR(64) NOT NULL,
    schema_ver   VARCHAR(32) NOT NULL,
    payload      JSONB NOT NULL,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_doc_parse_caches_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_doc_parse_caches_content_hash
    ON doc_parse_caches (content_hash);

CREATE INDEX IF NOT EXISTS idx_doc_parse_caches_tenant_parser
    ON doc_parse_caches (tenant_id, parser);

DO $$ BEGIN RAISE NOTICE '[Migration 000068] doc_parse_caches table ready'; END $$;

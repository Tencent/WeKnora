-- Migration: 000082_document_parse_cache
-- Store the latest resolved parser artifact for each knowledge so an unchanged
-- reparse can reuse Markdown and already-persisted image resources.

DO $$ BEGIN RAISE NOTICE '[Migration 000082] Creating table: document_parse_caches'; END $$;

CREATE TABLE IF NOT EXISTS document_parse_caches (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    cache_key    VARCHAR(64) NOT NULL,
    content_key  VARCHAR(128) NOT NULL,
    config_hash  VARCHAR(64) NOT NULL,
    schema_ver   VARCHAR(32) NOT NULL,
    payload      JSONB NOT NULL,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_document_parse_caches_knowledge UNIQUE (tenant_id, knowledge_id),
    CONSTRAINT fk_document_parse_caches_knowledge FOREIGN KEY (knowledge_id)
        REFERENCES knowledges(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_document_parse_caches_key
    ON document_parse_caches (tenant_id, cache_key);

DO $$ BEGIN RAISE NOTICE '[Migration 000082] document_parse_caches table ready'; END $$;

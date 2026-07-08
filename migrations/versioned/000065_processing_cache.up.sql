-- Migration: 000065_processing_cache
-- Description: Durable deterministic cache for expensive processing stages.

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating processing cache table'; END $$;

CREATE TABLE IF NOT EXISTS processing_caches (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    stage       VARCHAR(64) NOT NULL,
    cache_key   VARCHAR(128) NOT NULL,
    payload     JSONB NOT NULL DEFAULT '{}'::JSONB,
    metadata    JSONB NOT NULL DEFAULT '{}'::JSONB,
    last_hit_at TIMESTAMP WITH TIME ZONE,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_processing_cache_tenant_stage_key
    ON processing_caches(tenant_id, stage, cache_key);

CREATE INDEX IF NOT EXISTS idx_processing_cache_stage_updated
    ON processing_caches(stage, updated_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] processing cache table ready'; END $$;

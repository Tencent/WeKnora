-- Migration: 000065_multimodal_result_cache
--
-- Cache successful deterministic VLM outputs for image OCR/caption so unchanged
-- images do not burn VLM calls on document reparse.

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating table: multimodal_result_caches'; END $$;

CREATE TABLE IF NOT EXISTS multimodal_result_caches (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    cache_key   VARCHAR(64) NOT NULL,
    image_hash  VARCHAR(64) NOT NULL,
    model_id    VARCHAR(128) NOT NULL DEFAULT '',
    prompt_hash VARCHAR(64) NOT NULL,
    output_type VARCHAR(32) NOT NULL,
    schema_ver  VARCHAR(32) NOT NULL,
    content     TEXT NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_multimodal_result_caches_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_multimodal_result_caches_image_hash
    ON multimodal_result_caches (image_hash);

CREATE INDEX IF NOT EXISTS idx_multimodal_result_caches_tenant_output
    ON multimodal_result_caches (tenant_id, output_type);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] multimodal_result_caches table ready'; END $$;

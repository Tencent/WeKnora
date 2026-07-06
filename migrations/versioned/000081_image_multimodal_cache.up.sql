-- Migration: 000081_image_multimodal_cache
--
-- Cache deterministic OCR/caption outputs for document images. The key is
-- derived from image bytes + VLM model/config + prompt schema so reparses can
-- reuse expensive VLM calls without coupling cache rows to chunk IDs.

DO $$ BEGIN RAISE NOTICE '[Migration 000081] Creating table: image_multimodal_caches'; END $$;

CREATE TABLE IF NOT EXISTS image_multimodal_caches (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    cache_key   VARCHAR(64) NOT NULL,
    content_key VARCHAR(64) NOT NULL,
    model_id    VARCHAR(128) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    schema_ver  VARCHAR(32) NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_image_multimodal_caches_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_content_key
    ON image_multimodal_caches (content_key);

CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_tenant_model
    ON image_multimodal_caches (tenant_id, model_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000081] image_multimodal_caches table ready'; END $$;

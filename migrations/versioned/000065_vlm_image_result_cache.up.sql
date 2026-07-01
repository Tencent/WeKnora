DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating vlm_image_result_cache...'; END $$;

CREATE TABLE IF NOT EXISTS vlm_image_result_cache (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    image_hash VARCHAR(64) NOT NULL,
    model_fingerprint VARCHAR(64) NOT NULL,
    result_type VARCHAR(32) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    prompt_hash VARCHAR(64) NOT NULL,
    result_canonicalizer_version VARCHAR(64) NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_vlm_image_result_cache_tenant_key UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_vlm_image_result_cache_image_hash ON vlm_image_result_cache(image_hash);
CREATE INDEX IF NOT EXISTS idx_vlm_image_result_cache_model_fp ON vlm_image_result_cache(model_fingerprint);
CREATE INDEX IF NOT EXISTS idx_vlm_image_result_cache_result_type ON vlm_image_result_cache(result_type);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] vlm_image_result_cache ready'; END $$;

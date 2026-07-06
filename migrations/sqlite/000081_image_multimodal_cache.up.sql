-- Migration: 000081_image_multimodal_cache
CREATE TABLE IF NOT EXISTS image_multimodal_caches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    content_key VARCHAR(64) NOT NULL,
    model_id VARCHAR(128) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    schema_ver VARCHAR(32) NOT NULL,
    payload TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_content_key
    ON image_multimodal_caches(content_key);

CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_tenant_model
    ON image_multimodal_caches(tenant_id, model_id);

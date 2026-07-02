CREATE TABLE IF NOT EXISTS graph_extraction_caches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    content_key VARCHAR(64) NOT NULL,
    model_id VARCHAR(128) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    schema_ver VARCHAR(32) NOT NULL,
    graph TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_content_key
    ON graph_extraction_caches(content_key);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_tenant_model
    ON graph_extraction_caches(tenant_id, model_id);

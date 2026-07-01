CREATE TABLE IF NOT EXISTS wiki_map_caches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    content_key VARCHAR(64) NOT NULL,
    model_id VARCHAR(128) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    schema_ver VARCHAR(32) NOT NULL,
    payload TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_kind_content
    ON wiki_map_caches(kind, content_key);

CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_tenant_kind_model
    ON wiki_map_caches(tenant_id, kind, model_id);

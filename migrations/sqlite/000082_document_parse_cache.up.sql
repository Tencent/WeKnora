-- Migration: 000082_document_parse_cache
CREATE TABLE IF NOT EXISTS document_parse_caches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    content_key VARCHAR(128) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    schema_ver VARCHAR(32) NOT NULL,
    payload TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, knowledge_id),
    FOREIGN KEY (knowledge_id) REFERENCES knowledges(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_document_parse_caches_key
    ON document_parse_caches(tenant_id, cache_key);

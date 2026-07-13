-- Migration 000067: cache reusable reparse artifacts.
CREATE TABLE IF NOT EXISTS embedding_caches (
    cache_key VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    model_name VARCHAR(255) NOT NULL DEFAULT '',
    dimensions INTEGER NOT NULL,
    embedding JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_embedding_caches_tenant ON embedding_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_embedding_caches_content_hash ON embedding_caches (content_hash);
CREATE INDEX IF NOT EXISTS idx_embedding_caches_model_dim ON embedding_caches (model_id, dimensions);

CREATE TABLE IF NOT EXISTS image_multimodal_caches (
    cache_key VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    image_hash VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    model_name VARCHAR(255) NOT NULL DEFAULT '',
    prompt_version VARCHAR(64) NOT NULL,
    source_type VARCHAR(32) NOT NULL DEFAULT '',
    ocr_text TEXT NOT NULL DEFAULT '',
    caption TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_tenant ON image_multimodal_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_image_hash ON image_multimodal_caches (image_hash);
CREATE INDEX IF NOT EXISTS idx_image_multimodal_caches_model_prompt ON image_multimodal_caches (model_id, prompt_version);

CREATE TABLE IF NOT EXISTS graph_extraction_caches (
    cache_key VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    chunk_hash VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    model_name VARCHAR(255) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    graph_data JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_tenant ON graph_extraction_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_chunk_hash ON graph_extraction_caches (chunk_hash);
CREATE INDEX IF NOT EXISTS idx_graph_extraction_caches_model_config ON graph_extraction_caches (model_id, config_hash);

CREATE TABLE IF NOT EXISTS wiki_map_caches (
    cache_key VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    model_name VARCHAR(255) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    result_json JSONB NOT NULL,
    updates_json JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_tenant ON wiki_map_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_knowledge ON wiki_map_caches (knowledge_id);
CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_content_hash ON wiki_map_caches (content_hash);
CREATE INDEX IF NOT EXISTS idx_wiki_map_caches_model_config ON wiki_map_caches (model_id, config_hash);

CREATE TABLE IF NOT EXISTS reparse_artifact_caches (
    cache_key VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    artifact_type VARCHAR(32) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    model_name VARCHAR(255) NOT NULL DEFAULT '',
    config_hash VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    result_data BYTEA NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reparse_artifact_caches_tenant ON reparse_artifact_caches (tenant_id);
CREATE INDEX IF NOT EXISTS idx_reparse_artifact_caches_type ON reparse_artifact_caches (artifact_type);
CREATE INDEX IF NOT EXISTS idx_reparse_artifact_caches_content ON reparse_artifact_caches (content_hash);
CREATE INDEX IF NOT EXISTS idx_reparse_artifact_caches_model_config ON reparse_artifact_caches (model_id, config_hash);

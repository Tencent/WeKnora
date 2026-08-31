-- Restore the previous schema default. Existing rows intentionally remain
-- normalized because the data migration is not safely reversible.

CREATE TABLE knowledge_bases_new (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INTEGER NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config TEXT NOT NULL DEFAULT '{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}',
    image_processing_config TEXT NOT NULL DEFAULT '{"enable_multimodal": false, "model_id": ""}',
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config TEXT NOT NULL DEFAULT '{}',
    storage_provider_config TEXT DEFAULT NULL,
    vlm_config TEXT NOT NULL DEFAULT '{}',
    extract_config TEXT NULL DEFAULT NULL,
    faq_config TEXT,
    question_generation_config TEXT NULL,
    is_temporary BOOLEAN NOT NULL DEFAULT 0,
    is_pinned INTEGER NOT NULL DEFAULT 0,
    pinned_at DATETIME NULL,
    asr_config TEXT,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    creator_id VARCHAR(36),
    wiki_config TEXT,
    indexing_strategy TEXT DEFAULT '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    auto_tag_config TEXT
);

INSERT INTO knowledge_bases_new (
    id,
    name,
    description,
    tenant_id,
    type,
    chunking_config,
    image_processing_config,
    embedding_model_id,
    summary_model_id,
    cos_config,
    storage_provider_config,
    vlm_config,
    extract_config,
    faq_config,
    question_generation_config,
    is_temporary,
    is_pinned,
    pinned_at,
    asr_config,
    vector_store_id,
    storage_backend_id,
    creator_id,
    wiki_config,
    indexing_strategy,
    created_at,
    updated_at,
    deleted_at,
    auto_tag_config
)
SELECT
    id,
    name,
    description,
    tenant_id,
    type,
    chunking_config,
    image_processing_config,
    embedding_model_id,
    summary_model_id,
    cos_config,
    storage_provider_config,
    vlm_config,
    extract_config,
    faq_config,
    question_generation_config,
    is_temporary,
    is_pinned,
    pinned_at,
    asr_config,
    vector_store_id,
    storage_backend_id,
    creator_id,
    wiki_config,
    indexing_strategy,
    created_at,
    updated_at,
    deleted_at,
    auto_tag_config
FROM knowledge_bases;

DROP TABLE knowledge_bases;
ALTER TABLE knowledge_bases_new RENAME TO knowledge_bases;

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_vector_store
    ON knowledge_bases(tenant_id, vector_store_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_storage_backend
    ON knowledge_bases(tenant_id, storage_backend_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_creator
    ON knowledge_bases(tenant_id, creator_id);

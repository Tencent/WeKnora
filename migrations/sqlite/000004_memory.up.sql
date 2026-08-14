-- Cross-session long-term memory (Lite). Mirrors migrations/versioned/000084.
-- Row ids are generated in Go, so there is no server-side default here.

CREATE TABLE IF NOT EXISTS memory_subjects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    block_text TEXT NOT NULL DEFAULT '',
    block_updated_at DATETIME,
    item_count INTEGER NOT NULL DEFAULT 0,
    last_extracted_at DATETIME,
    extract_cursor DATETIME,
    pending_sessions TEXT,
    extract_scheduled_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_subjects_scope
    ON memory_subjects (tenant_id, subject_id);

CREATE TABLE IF NOT EXISTS memory_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    content TEXT NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    normalized_key VARCHAR(255) NOT NULL DEFAULT '',
    importance INTEGER NOT NULL DEFAULT 3,
    origin VARCHAR(16) NOT NULL DEFAULT 'extracted',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    source_session_id VARCHAR(36),
    source_message_id VARCHAR(36),
    valid_from DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    invalid_at DATETIME,
    expires_at DATETIME,
    superseded_by VARCHAR(36),
    last_used_at DATETIME,
    use_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_items_scope
    ON memory_items (tenant_id, subject_id, status);
CREATE INDEX IF NOT EXISTS idx_memory_items_key
    ON memory_items (tenant_id, subject_id, normalized_key);

CREATE TABLE IF NOT EXISTS memory_tombstones (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    fingerprint VARCHAR(64) NOT NULL,
    source_message_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_tombstones_scope
    ON memory_tombstones (tenant_id, subject_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_tomb_fp
    ON memory_tombstones (tenant_id, subject_id, fingerprint);

CREATE TABLE IF NOT EXISTS memory_topic_stats (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    normalized_key VARCHAR(255) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    hits INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    promoted_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_topic_scope
    ON memory_topic_stats (tenant_id, subject_id, normalized_key);

CREATE TABLE IF NOT EXISTS memory_doc_affinity (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    title VARCHAR(512) NOT NULL DEFAULT '',
    hits INTEGER NOT NULL DEFAULT 0,
    last_used_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_affinity_scope
    ON memory_doc_affinity (tenant_id, subject_id, knowledge_id);

ALTER TABLE tenants ADD COLUMN memory_config TEXT;
ALTER TABLE messages ADD COLUMN used_memories TEXT;

ALTER TABLE memory_subjects ADD COLUMN consolidated_at DATETIME;
ALTER TABLE memory_subjects ADD COLUMN forced_consolidated_at DATETIME;

ALTER TABLE memory_topic_stats ADD COLUMN aliases TEXT NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS memory_item_embeddings (
    item_id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    dims INTEGER NOT NULL DEFAULT 0,
    vector BLOB,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_mem_emb_scope
    ON memory_item_embeddings (tenant_id, subject_id);

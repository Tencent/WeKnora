-- Migration 000084: cross-session long-term memory.
CREATE TABLE memory_subjects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    block_text TEXT NOT NULL DEFAULT (''),
    block_updated_at DATETIME(6),
    item_count INTEGER NOT NULL DEFAULT 0,
    last_extracted_at DATETIME(6),
    extract_cursor DATETIME(6),
    pending_sessions JSON,
    extract_scheduled_at DATETIME(6),
    consolidated_at DATETIME(6),
    forced_consolidated_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_memory_subjects_scope (tenant_id, subject_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE memory_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    content TEXT NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    normalized_key VARCHAR(255) NOT NULL DEFAULT '',
    importance SMALLINT NOT NULL DEFAULT 3,
    origin VARCHAR(16) NOT NULL DEFAULT 'extracted',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    source_session_id VARCHAR(36),
    source_message_id VARCHAR(36),
    valid_from DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    invalid_at DATETIME(6),
    expires_at DATETIME(6),
    superseded_by VARCHAR(36),
    last_used_at DATETIME(6),
    use_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_memory_items_scope (tenant_id, subject_id, status),
    KEY idx_memory_items_key (tenant_id, subject_id(384), normalized_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE memory_tombstones (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    fingerprint VARCHAR(64) NOT NULL,
    source_message_id VARCHAR(36),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_memory_tombstones_scope (tenant_id, subject_id),
    UNIQUE KEY idx_mem_tomb_fp (tenant_id, subject_id, fingerprint)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE memory_topic_stats (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    normalized_key VARCHAR(255) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    aliases JSON NOT NULL DEFAULT (JSON_ARRAY()),
    hits INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME(6),
    promoted_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_mem_topic_scope (tenant_id, subject_id(384), normalized_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE memory_doc_affinity (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    title VARCHAR(512) NOT NULL DEFAULT '',
    hits INTEGER NOT NULL DEFAULT 0,
    last_used_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_mem_affinity_scope (tenant_id, subject_id, knowledge_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE tenants ADD COLUMN memory_config JSON;
ALTER TABLE messages ADD COLUMN used_memories JSON;

CREATE TABLE memory_item_embeddings (
    item_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    dims INTEGER NOT NULL DEFAULT 0,
    vector LONGBLOB,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_mem_emb_scope (tenant_id, subject_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

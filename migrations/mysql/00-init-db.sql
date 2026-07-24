DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS knowledge_bases;
DROP TABLE IF EXISTS knowledges;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS chunks;

CREATE TABLE tenants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    retriever_engines JSON NOT NULL,
    status VARCHAR(50) DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    agent_config JSON DEFAULT NULL COMMENT 'Tenant-level agent configuration in JSON format',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=10000;

CREATE TABLE models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;  

CREATE INDEX idx_models_tenant_source_type ON models(tenant_id, source, type);

CREATE TABLE knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INT NOT NULL,
    chunking_config JSON NOT NULL,
    image_processing_config JSON NOT NULL,
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    rerank_model_id VARCHAR(64) NOT NULL,
    cos_config JSON NOT NULL,
    vlm_config JSON NOT NULL,
    extract_config JSON NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledge_bases_tenant_name ON knowledge_bases(tenant_id, name);

CREATE TABLE knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    source VARCHAR(2048) NOT NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64),
    file_name VARCHAR(255),
    file_type VARCHAR(50),
    file_size BIGINT,
    file_path TEXT,
    file_hash VARCHAR(64),
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata JSON,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    processed_at TIMESTAMP,
    error_message TEXT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledges_tenant_id ON knowledges(tenant_id, knowledge_base_id);

CREATE TABLE sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    title VARCHAR(255),
    description TEXT,
    knowledge_base_id VARCHAR(36),
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT TRUE,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response VARCHAR(255) NOT NULL DEFAULT '很抱歉，我暂时无法回答这个问题。',
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters JSON NOT NULL,
    agent_config JSON DEFAULT NULL COMMENT 'Session-level agent configuration in JSON format',
    context_config JSON DEFAULT NULL COMMENT 'LLM context management configuration (separate from message storage)',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);

CREATE TABLE messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    knowledge_references JSON NOT NULL,
    agent_steps JSON DEFAULT NULL COMMENT 'Agent execution steps (reasoning process and tool calls)',
    is_completed BOOLEAN NOT NULL DEFAULT FALSE,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INTEGER NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_messages_session_role ON messages(session_id, role); 
CREATE INDEX idx_messages_agent_id ON messages(agent_id);

CREATE TABLE message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INTEGER NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    allow_regenerate BOOLEAN NOT NULL DEFAULT FALSE,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions JSON NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMP NULL DEFAULT NULL,
    generated_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY idx_message_suggestion_sets_cache_key
        (tenant_id, assistant_message_id, placement, config_hash, locale),
    KEY idx_message_suggestion_sets_session (tenant_id, session_id, created_at),
    KEY idx_message_suggestion_sets_status (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE message_suggestion_events (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_message_suggestion_events_set (suggestion_set_id, created_at),
    KEY idx_message_suggestion_events_session (tenant_id, session_id, created_at),
    KEY idx_message_suggestion_events_type (event_type, created_at),
    CONSTRAINT fk_message_suggestion_events_set
        FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    start_at INTEGER NOT NULL,
    end_at INTEGER NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info TEXT,
    relation_chunks JSON,
    indirect_relation_chunks JSON,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_chunks_tenant_knowledge ON chunks(tenant_id, knowledge_id);
CREATE INDEX idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX idx_chunks_chunk_type ON chunks(chunk_type);

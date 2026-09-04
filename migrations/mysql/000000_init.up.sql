-- Migration: 000000_init (MySQL)
-- Description: Core relational metadata schema for WeKnora running on MySQL.
--
-- This is the MySQL counterpart of migrations/versioned/000000_init.up.sql
-- (PostgreSQL) and migrations/sqlite/000000_init.up.sql (SQLite). It covers
-- only the 15 core tables needed for the MVP; remaining tables (tags / MCP /
-- wiki / auth_tokens / tenant_api_keys / audit_logs / ...) are added by
-- follow-up migrations.
--
-- Type mapping rules:
--   SERIAL / BIGSERIAL                 -> BIGINT AUTO_INCREMENT PRIMARY KEY
--   JSONB / TEXT (JSON semantics)      -> JSON
--   TIMESTAMP WITH TIME ZONE / DATETIME -> DATETIME(3)
--   TEXT (long content)                -> LONGTEXT
--   uuid_generate_v4() default         -> dropped (UUIDs are app-generated)
--   vector / BM25 / HNSW indexes       -> not created (vector search is external)
--
-- Requires MySQL 8.0.13+ (expression defaults such as DEFAULT (JSON_OBJECT()) /
-- DEFAULT ('') are used for JSON/TEXT columns that need a DB-side default).

CREATE TABLE IF NOT EXISTS tenants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    retriever_engines JSON NOT NULL,
    status VARCHAR(50) DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    agent_config JSON DEFAULT NULL,
    context_config JSON DEFAULT NULL,
    conversation_config JSON DEFAULT NULL,
    web_search_config JSON DEFAULT NULL,
    parser_engine_config JSON DEFAULT NULL,
    storage_engine_config JSON DEFAULT NULL,
    default_storage_backend_id VARCHAR(36),
    credentials JSON DEFAULT NULL,
    chat_history_config JSON DEFAULT NULL,
    retrieval_config JSON DEFAULT NULL,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=10000;

CREATE INDEX idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description LONGTEXT,
    parameters JSON NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    managed_by VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_models_type ON models(type);
CREATE INDEX idx_models_source ON models(source);
CREATE INDEX idx_models_is_builtin ON models(is_builtin);
CREATE INDEX idx_models_managed_by ON models(managed_by);

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    tenant_id BIGINT NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config JSON NOT NULL,
    image_processing_config JSON NOT NULL,
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config JSON NOT NULL,
    storage_provider_config JSON DEFAULT NULL,
    vlm_config JSON NOT NULL,
    extract_config JSON DEFAULT NULL,
    faq_config JSON DEFAULT NULL,
    question_generation_config JSON DEFAULT NULL,
    is_temporary BOOLEAN NOT NULL DEFAULT FALSE,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(3) NULL,
    asr_config JSON DEFAULT NULL,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    creator_id VARCHAR(36),
    wiki_config JSON DEFAULT NULL,
    indexing_strategy JSON DEFAULT NULL,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX idx_knowledge_bases_tenant_vector_store ON knowledge_bases(tenant_id, vector_store_id);
CREATE INDEX idx_knowledge_bases_storage_backend ON knowledge_bases(tenant_id, storage_backend_id);
CREATE INDEX idx_knowledge_bases_tenant_creator ON knowledge_bases(tenant_id, creator_id);

CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description LONGTEXT,
    source VARCHAR(2048) NOT NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64),
    file_name VARCHAR(255),
    file_type VARCHAR(50),
    file_size BIGINT,
    file_path LONGTEXT,
    file_hash VARCHAR(64),
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata JSON DEFAULT NULL,
    custom_metadata JSON NOT NULL,
    tag_id VARCHAR(36),
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result JSON DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    processed_at DATETIME(3),
    error_message LONGTEXT,
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX idx_knowledges_enable_status ON knowledges(enable_status);
CREATE INDEX idx_knowledges_tag ON knowledges(tag_id);
CREATE INDEX idx_knowledges_summary_status ON knowledges(summary_status);

CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    title VARCHAR(255),
    description LONGTEXT,
    knowledge_base_id VARCHAR(36),
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT TRUE,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response VARCHAR(255) NOT NULL DEFAULT '很抱歉，我暂时无法回答这个问题。',
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INT NOT NULL DEFAULT 10,
    rerank_top_k INT NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters JSON NOT NULL,
    agent_config JSON DEFAULT NULL,
    context_config JSON DEFAULT NULL,
    agent_id VARCHAR(36),
    user_id VARCHAR(512),
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(3),
    sandbox_config_id VARCHAR(36),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);
CREATE INDEX idx_sessions_tenant_user_pin ON sessions(tenant_id, user_id, is_pinned, pinned_at, updated_at);

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content LONGTEXT NOT NULL,
    rendered_content LONGTEXT NOT NULL DEFAULT (''),
    knowledge_references JSON NOT NULL,
    agent_steps JSON DEFAULT NULL,
    mentioned_items JSON DEFAULT NULL,
    images JSON DEFAULT NULL,
    artifacts JSON DEFAULT NULL,
    is_completed BOOLEAN NOT NULL DEFAULT FALSE,
    is_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id BIGINT NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context JSON NOT NULL,
    agent_duration_ms INT DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_messages_session_id ON messages(session_id);
CREATE INDEX idx_messages_knowledge_id ON messages(knowledge_id);
CREATE INDEX idx_messages_agent_id ON messages(agent_id);

CREATE TABLE IF NOT EXISTS message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id BIGINT NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    allow_regenerate BOOLEAN NOT NULL DEFAULT FALSE,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions JSON NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until DATETIME(3) NULL,
    generated_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY idx_message_suggestion_sets_cache_key
        (tenant_id, assistant_message_id, placement, config_hash, locale),
    KEY idx_message_suggestion_sets_session (tenant_id, session_id, created_at),
    KEY idx_message_suggestion_sets_status (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS message_suggestion_events (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_message_suggestion_events_set (suggestion_set_id, created_at),
    KEY idx_message_suggestion_events_session (tenant_id, session_id, created_at),
    KEY idx_message_suggestion_events_type (event_type, created_at),
    CONSTRAINT fk_message_suggestion_events_set
        FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content LONGTEXT NOT NULL,
    source_content LONGTEXT NOT NULL,
    content_revision INT NOT NULL DEFAULT 0,
    index_status VARCHAR(16) NOT NULL DEFAULT 'ready',
    last_editor_id VARCHAR(64) NOT NULL DEFAULT '',
    context_header LONGTEXT NOT NULL,
    chunk_index INT NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    start_at INT NOT NULL,
    end_at INT NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info LONGTEXT,
    video_info LONGTEXT,
    relation_chunks JSON DEFAULT NULL,
    indirect_relation_chunks JSON DEFAULT NULL,
    metadata JSON DEFAULT NULL,
    tag_id VARCHAR(36),
    status INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64),
    flags INT NOT NULL DEFAULT 1,
    seq_id BIGINT NOT NULL AUTO_INCREMENT,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3),
    KEY idx_chunks_tenant_kg (tenant_id, knowledge_id),
    KEY idx_chunks_parent_id (parent_chunk_id),
    KEY idx_chunks_chunk_type (chunk_type),
    KEY idx_chunks_tag (tag_id),
    KEY idx_chunks_content_hash (content_hash),
    UNIQUE KEY idx_chunks_seq_id (seq_id),
    KEY idx_chunks_kb_tenant (knowledge_base_id, tenant_id),
    KEY idx_chunks_knowledge_enabled (knowledge_id, is_enabled, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=100000000;

CREATE TABLE IF NOT EXISTS chunk_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    revision INT NOT NULL,
    content LONGTEXT NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    editor_id VARCHAR(64) NOT NULL DEFAULT '',
    edit_source VARCHAR(16) NOT NULL DEFAULT 'user',
    edited_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY idx_chunk_revisions_chunk_revision (chunk_id, revision),
    KEY idx_chunk_revisions_tenant_chunk (tenant_id, chunk_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id BIGINT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT FALSE,
    is_system_admin BOOLEAN NOT NULL DEFAULT FALSE,
    preferences JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3),
    UNIQUE KEY idx_users_username (username),
    UNIQUE KEY idx_users_email (email),
    KEY idx_users_is_system_admin (is_system_admin),
    KEY idx_users_tenant_id (tenant_id),
    KEY idx_users_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id BIGINT NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval BOOLEAN DEFAULT FALSE,
    invite_code_expires_at DATETIME(3),
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT FALSE,
    member_limit INT NOT NULL DEFAULT 50,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3),
    KEY idx_organizations_owner_id (owner_id),
    KEY idx_organizations_owner_tenant (owner_tenant_id),
    KEY idx_organizations_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tenant_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36),
    joined_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3),
    UNIQUE KEY idx_tenant_members_user_tenant_unique (user_id, tenant_id),
    KEY idx_tenant_members_tenant_role (tenant_id, role),
    KEY idx_tenant_members_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS system_settings (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    `key` VARCHAR(128) NOT NULL,
    value JSON NOT NULL,
    value_type VARCHAR(16) NOT NULL,
    category VARCHAR(32) NOT NULL,
    description LONGTEXT NOT NULL DEFAULT (''),
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    requires_restart BOOLEAN NOT NULL DEFAULT FALSE,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY idx_system_settings_key (`key`),
    KEY idx_system_settings_category (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS task_pending_ops (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload JSON NOT NULL DEFAULT (JSON_OBJECT()),
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    claimed_at DATETIME(3),
    KEY idx_task_pending_ops_scope (task_type, scope, scope_id, id),
    KEY idx_task_pending_ops_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

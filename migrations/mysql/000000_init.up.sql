-- Migration: 000000_init
-- Description: Initialize MySQL database schema for WeKnora
-- Engine: InnoDB, Charset: utf8mb4

-- ============================================================================
-- Tenants
-- ============================================================================
CREATE TABLE IF NOT EXISTS tenants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    api_key VARCHAR(512) NOT NULL,
    retriever_engines JSON NOT NULL,
    status VARCHAR(50) DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    context_config JSON DEFAULT NULL,
    web_search_config JSON DEFAULT NULL,
    parser_engine_config JSON DEFAULT NULL,
    credentials JSON DEFAULT NULL,
    storage_engine_config JSON DEFAULT NULL,
    chat_history_config JSON DEFAULT NULL,
    retrieval_config JSON DEFAULT NULL,
    api_principal_config JSON DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=10000; -- start at 10000 to match PostgreSQL sequence offset

CREATE INDEX idx_tenants_api_key ON tenants(api_key);
CREATE INDEX idx_tenants_status ON tenants(status);

-- ============================================================================
-- Users
-- ============================================================================
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500) DEFAULT NULL,
    tenant_id BIGINT DEFAULT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT FALSE,
    is_system_admin BOOLEAN NOT NULL DEFAULT FALSE,
    preferences JSON NOT NULL DEFAULT ('{}'),
    source VARCHAR(50) DEFAULT 'password',
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    UNIQUE KEY uk_users_username (username),
    UNIQUE KEY uk_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_deleted_at ON users(deleted_at);
CREATE INDEX idx_users_is_active ON users(is_active);
-- Performance note: application code queries users via LOWER(username/email)
-- for case-insensitive search. For MySQL tables exceeding ~10k rows, add:
--   CREATE INDEX idx_users_email_lower ON users((LOWER(email)));
--   CREATE INDEX idx_users_username_lower ON users((LOWER(username)));

-- ============================================================================
-- Auth Tokens
-- ============================================================================
CREATE TABLE IF NOT EXISTS auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token TEXT NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX idx_auth_tokens_token ON auth_tokens(token(255));
CREATE INDEX idx_auth_tokens_token_type ON auth_tokens(token_type);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

-- ============================================================================
-- Models
-- ============================================================================
CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    managed_by VARCHAR(255) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_models_type ON models(type);
CREATE INDEX idx_models_source ON models(source);
CREATE INDEX idx_models_tenant_source_type ON models(tenant_id, source, type);

-- ============================================================================
-- Knowledge Bases
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    is_temporary BOOLEAN NOT NULL DEFAULT FALSE,
    description TEXT,
    tenant_id BIGINT NOT NULL,
    creator_id VARCHAR(36) DEFAULT NULL,
    chunking_config JSON NOT NULL,
    image_processing_config JSON NOT NULL,
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    rerank_model_id VARCHAR(64) NOT NULL,
    cos_config JSON NOT NULL,
    vlm_config JSON NOT NULL,
    asr_config JSON DEFAULT NULL,
    extract_config JSON DEFAULT NULL,
    faq_config JSON DEFAULT NULL,
    question_generation_config JSON DEFAULT NULL,
    wiki_config JSON DEFAULT NULL,
    indexing_strategy JSON DEFAULT NULL,
    storage_provider_config JSON DEFAULT NULL,
    vector_store_id VARCHAR(36) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX idx_knowledge_bases_creator_id ON knowledge_bases(creator_id);

-- ============================================================================
-- Knowledges (Documents / FAQ entries)
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    source VARCHAR(2048) NOT NULL,
    channel VARCHAR(50) DEFAULT 'web',
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    summary_status VARCHAR(32) DEFAULT 'none',
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64) DEFAULT NULL,
    file_name VARCHAR(255) DEFAULT NULL,
    file_type VARCHAR(50) DEFAULT NULL,
    file_size BIGINT DEFAULT NULL,
    file_path TEXT,
    file_hash VARCHAR(64) DEFAULT NULL,
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata JSON,
    last_faq_import_result JSON,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    processed_at DATETIME(6) DEFAULT NULL,
    error_message TEXT,
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX idx_knowledges_enable_status ON knowledges(enable_status);

-- ============================================================================
-- Sessions
-- ============================================================================
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(512) DEFAULT NULL,
    title VARCHAR(255) DEFAULT NULL,
    description TEXT,
    knowledge_base_id VARCHAR(36) DEFAULT NULL,
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT TRUE,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response TEXT NOT NULL DEFAULT ('很抱歉，我暂时无法回答这个问题。'),
    keyword_threshold DOUBLE NOT NULL DEFAULT 0.5,
    vector_threshold DOUBLE NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64) DEFAULT NULL,
    embedding_top_k INT NOT NULL DEFAULT 10,
    rerank_top_k INT NOT NULL DEFAULT 10,
    rerank_threshold DOUBLE NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64) DEFAULT NULL,
    summary_parameters JSON NOT NULL,
    agent_config JSON DEFAULT NULL COMMENT 'Session-level agent configuration',
    context_config JSON DEFAULT NULL COMMENT 'LLM context management configuration',
    agent_id VARCHAR(36) DEFAULT NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(6) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);

-- ============================================================================
-- Messages
-- ============================================================================
CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    knowledge_references JSON NOT NULL,
    agent_steps JSON DEFAULT NULL,
    mentioned_items JSON DEFAULT NULL,
    images JSON DEFAULT NULL,
    attachments JSON DEFAULT NULL,
    is_completed BOOLEAN NOT NULL DEFAULT FALSE,
    is_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    agent_duration_ms BIGINT NOT NULL DEFAULT 0,
    rendered_content TEXT,
    channel VARCHAR(50) DEFAULT '',
    knowledge_id VARCHAR(36) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Chunks
-- ============================================================================
CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    chunk_index INT NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    flags INT NOT NULL DEFAULT 0,
    status INT NOT NULL DEFAULT 0,
    tag_id VARCHAR(36) DEFAULT NULL,
    seq_id BIGINT AUTO_INCREMENT UNIQUE,
    start_at INT NOT NULL,
    end_at INT NOT NULL,
    pre_chunk_id VARCHAR(36) DEFAULT NULL,
    next_chunk_id VARCHAR(36) DEFAULT NULL,
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36) DEFAULT NULL,
    image_info TEXT,
    relation_chunks JSON,
    indirect_relation_chunks JSON,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
CREATE INDEX idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX idx_chunks_chunk_type ON chunks(chunk_type);
CREATE INDEX idx_chunks_seq_id ON chunks(seq_id);

-- ============================================================================
-- Embeddings (table placeholder — MySQL mode uses external vector store)
-- Note: In MySQL mode, embeddings/vector search is handled by an external
-- retrieval engine. This table exists for schema completeness; actual vector
-- storage uses the configured RETRIEVE_DRIVER (Qdrant, Milvus, etc.).
-- Columns mirror the PostgreSQL embeddings table (000002_embeddings.up.sql)
-- except for pgvector-specific columns (halfvec) and PostgreSQL-only indexes.
-- ============================================================================
CREATE TABLE IF NOT EXISTS embeddings (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    source_id VARCHAR(64) NOT NULL,
    source_type INT NOT NULL,
    chunk_id VARCHAR(64) DEFAULT NULL,
    knowledge_id VARCHAR(64) DEFAULT NULL,
    knowledge_base_id VARCHAR(64) DEFAULT NULL,
    content TEXT,
    dimension INT NOT NULL DEFAULT 0,
    tenant_id BIGINT NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    tag_id VARCHAR(36) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_embeddings_source ON embeddings(source_id, source_type);
CREATE INDEX idx_embeddings_chunk ON embeddings(chunk_id);
CREATE INDEX idx_embeddings_kb ON embeddings(knowledge_base_id);
CREATE INDEX idx_embeddings_knowledge ON embeddings(knowledge_id);
CREATE INDEX idx_embeddings_tenant ON embeddings(tenant_id);
CREATE INDEX idx_embeddings_is_enabled ON embeddings(is_enabled);

-- ============================================================================
-- Custom Agents
-- ============================================================================
CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64) DEFAULT NULL,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    tenant_id BIGINT NOT NULL,
    created_by VARCHAR(36) DEFAULT NULL,
    config JSON NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    PRIMARY KEY (id, tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX idx_custom_agents_deleted_at ON custom_agents(deleted_at);

-- ============================================================================
-- Agent Shares
-- ============================================================================
CREATE TABLE IF NOT EXISTS agent_shares (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    target_tenant_id BIGINT DEFAULT NULL,
    target_org_id BIGINT DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_agent_shares_agent ON agent_shares(agent_id, tenant_id);

-- ============================================================================
-- Knowledge Tags
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    color VARCHAR(50) DEFAULT NULL,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    seq_id BIGINT AUTO_INCREMENT UNIQUE,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledge_tags_tenant_kb ON knowledge_tags(tenant_id, knowledge_base_id);

-- ============================================================================
-- Knowledge Tag Relations
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_knowledge_tag (knowledge_id, tag_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_ktr_tag ON knowledge_tag_relations(tag_id);

-- ============================================================================
-- IM (Instant Messaging) Channels
-- ============================================================================
CREATE TABLE IF NOT EXISTS im_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    knowledge_base_id VARCHAR(36) DEFAULT '',
    bot_identity VARCHAR(255) NOT NULL DEFAULT '',
    session_mode VARCHAR(20) NOT NULL DEFAULT 'user',
    credentials JSON NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    UNIQUE KEY uk_channel_bot_identity (bot_identity),
    INDEX idx_im_channels_tenant (tenant_id),
    INDEX idx_im_channels_agent (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- IM Channel Sessions (mapping IM conversations to sessions)
-- ============================================================================
CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    channel_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    thread_id VARCHAR(255) DEFAULT NULL,
    user_id VARCHAR(255) DEFAULT NULL,
    agent_id VARCHAR(36) DEFAULT NULL,
    bot_identity VARCHAR(255) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_ics_channel ON im_channel_sessions(channel_id);
CREATE INDEX idx_ics_session ON im_channel_sessions(session_id);

-- ============================================================================
-- Vector Stores (managed vector database connections)
-- ============================================================================
CREATE TABLE IF NOT EXISTS vector_stores (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config JSON NOT NULL,
    index_config JSON NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_vector_stores_tenant ON vector_stores(tenant_id);
CREATE INDEX idx_vector_stores_engine_type ON vector_stores(engine_type);

-- ============================================================================
-- Data Sources (for scheduled sync)
-- ============================================================================
CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    description TEXT,
    config JSON,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT 'incremental',
    status VARCHAR(32) DEFAULT 'active',
    conflict_strategy VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions BOOLEAN DEFAULT TRUE,
    last_sync_at DATETIME(6) DEFAULT NULL,
    last_sync_cursor JSON,
    last_sync_result JSON,
    error_message TEXT,
    sync_log_retention_days INT DEFAULT 30,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    INDEX idx_data_sources_tenant (tenant_id),
    INDEX idx_data_sources_kb (knowledge_base_id),
    INDEX idx_data_sources_type (type),
    INDEX idx_data_sources_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Sync Logs
-- ============================================================================
CREATE TABLE IF NOT EXISTS sync_logs (
    id VARCHAR(36) PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    started_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6) DEFAULT NULL,
    items_total INT NOT NULL DEFAULT 0,
    items_created INT NOT NULL DEFAULT 0,
    items_updated INT NOT NULL DEFAULT 0,
    items_deleted INT NOT NULL DEFAULT 0,
    items_skipped INT NOT NULL DEFAULT 0,
    items_failed INT NOT NULL DEFAULT 0,
    error_message TEXT,
    result JSON,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    INDEX idx_sync_logs_ds (data_source_id),
    INDEX idx_sync_logs_tenant (tenant_id),
    INDEX idx_sync_logs_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Web Search Providers
-- ============================================================================
CREATE TABLE IF NOT EXISTS web_search_providers (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    provider_type VARCHAR(50) NOT NULL,
    config JSON NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Organizations
-- ============================================================================
CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(512) DEFAULT '',
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id BIGINT NOT NULL DEFAULT 0,
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    searchable BOOLEAN NOT NULL DEFAULT FALSE,
    invite_code VARCHAR(32) DEFAULT NULL,
    invite_code_expires_at DATETIME(6) DEFAULT NULL,
    invite_code_validity_days INT NOT NULL DEFAULT 7,
    member_limit INT NOT NULL DEFAULT 50,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX idx_organizations_owner_tenant ON organizations(owner_tenant_id);

-- ============================================================================
-- Organization Members
-- ============================================================================
CREATE TABLE IF NOT EXISTS organization_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'member',
    joined_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    UNIQUE KEY uk_org_user (organization_id, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Organization Join Requests
-- ============================================================================
CREATE TABLE IF NOT EXISTS organization_join_requests (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32) DEFAULT NULL,
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    message TEXT,
    reviewed_by VARCHAR(36) DEFAULT NULL,
    reviewed_at DATETIME(6) DEFAULT NULL,
    review_message TEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    INDEX idx_ojr_org (organization_id),
    INDEX idx_ojr_user (user_id),
    INDEX idx_ojr_type (request_type),
    INDEX idx_ojr_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Tenant Members
-- ============================================================================
CREATE TABLE IF NOT EXISTS tenant_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36) DEFAULT NULL,
    joined_at DATETIME(6) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    INDEX idx_tenant_members_user (user_id),
    INDEX idx_tenant_members_tenant (tenant_id),
    INDEX idx_tenant_members_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Organization-Tenant Members
-- ============================================================================
CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at DATETIME(6) DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    INDEX idx_otm_org (organization_id),
    INDEX idx_otm_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Tenant Invitations
-- ============================================================================
CREATE TABLE IF NOT EXISTS tenant_invitations (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL DEFAULT '',
    token VARCHAR(64) NOT NULL DEFAULT '',
    invited_by VARCHAR(36) DEFAULT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'viewer',
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    message VARCHAR(500) DEFAULT NULL,
    expires_at DATETIME(6) NOT NULL,
    responded_at DATETIME(6) DEFAULT NULL,
    accepted_count INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    INDEX idx_ti_tenant (tenant_id),
    INDEX idx_ti_token (token),
    INDEX idx_ti_invitee (invitee_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Tenant Disabled Shared Agents
-- ============================================================================
CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_tdsa_tenant_agent (tenant_id, agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Wiki Pages
-- ============================================================================
CREATE TABLE IF NOT EXISTS wiki_pages (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    title VARCHAR(512) NOT NULL DEFAULT '',
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary',
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    content TEXT,
    summary TEXT NOT NULL DEFAULT (''),
    aliases JSON DEFAULT NULL,
    parent_slug VARCHAR(255) NOT NULL DEFAULT '',
    folder_id VARCHAR(36) DEFAULT NULL,
    category_path JSON DEFAULT NULL,
    wiki_path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    source_refs JSON DEFAULT (CAST('[]' AS JSON)),
    chunk_refs JSON DEFAULT (CAST('[]' AS JSON)),
    in_links JSON DEFAULT (CAST('[]' AS JSON)),
    out_links JSON DEFAULT (CAST('[]' AS JSON)),
    page_metadata JSON DEFAULT NULL,
    version INT NOT NULL DEFAULT 1,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_wp_tenant_kb ON wiki_pages(tenant_id, knowledge_base_id);
CREATE INDEX idx_wp_folder ON wiki_pages(folder_id);
CREATE INDEX idx_wp_page_type ON wiki_pages(page_type);
CREATE INDEX idx_wp_status ON wiki_pages(status);
CREATE INDEX idx_wp_slug ON wiki_pages(knowledge_base_id, slug);
CREATE INDEX idx_wp_wiki_path ON wiki_pages(wiki_path);
CREATE INDEX idx_wp_sort_order ON wiki_pages(sort_order);
-- Performance note: for MySQL full-text wiki search, uncomment the next line:
-- ALTER TABLE wiki_pages ADD FULLTEXT INDEX ft_wiki_search (title, content, summary, slug);
-- Or create it preemptively since MySQL 8.0 supports InnoDB FULLTEXT natively:
-- CREATE FULLTEXT INDEX ft_wiki_search ON wiki_pages(title, content, summary, slug);

-- ============================================================================
-- Wiki Folders
-- ============================================================================
CREATE TABLE IF NOT EXISTS wiki_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_wf_tenant_kb ON wiki_folders(tenant_id, knowledge_base_id);
CREATE INDEX idx_wf_parent ON wiki_folders(parent_id);

-- ============================================================================
-- Wiki Log Entries
-- ============================================================================
CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id VARCHAR(36) NOT NULL,
    action VARCHAR(50) NOT NULL,
    user_id VARCHAR(36) DEFAULT NULL,
    detail JSON DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_wle_page ON wiki_log_entries(page_id);
CREATE INDEX idx_wle_tenant_kb ON wiki_log_entries(tenant_id, knowledge_base_id);

-- ============================================================================
-- Wiki Page Issues
-- ============================================================================
CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id VARCHAR(36) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'open',
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_wpi_page ON wiki_page_issues(page_id);

-- ============================================================================
-- Task Pending Ops (background job queue)
-- ============================================================================
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload JSON NOT NULL,
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    claimed_at DATETIME(6) DEFAULT NULL,
    INDEX idx_tpo_scope (task_type, scope, scope_id, id),
    INDEX idx_tpo_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Task Dead Letters
-- ============================================================================
CREATE TABLE IF NOT EXISTS task_dead_letters (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL DEFAULT 'unknown',
    scope_id VARCHAR(64) NOT NULL DEFAULT '',
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    payload JSON NOT NULL,
    last_error TEXT,
    fail_count INT NOT NULL DEFAULT 0,
    failed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_tdl_tenant (tenant_id),
    INDEX idx_tdl_scope (scope, scope_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- MCP Services
-- ============================================================================
CREATE TABLE IF NOT EXISTS mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512) DEFAULT NULL,
    headers JSON DEFAULT NULL,
    auth_config JSON DEFAULT NULL,
    advanced_config JSON DEFAULT NULL,
    stdio_config JSON DEFAULT NULL,
    env_vars JSON DEFAULT NULL,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    INDEX idx_mcp_services_tenant (tenant_id),
    UNIQUE KEY uk_mcp_name_tenant (tenant_id, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- MCP Tool Approvals
-- ============================================================================
CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(255) NOT NULL,
    approved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_mcp_approval (tenant_id, user_id, tool_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- MCP OAuth Clients
-- ============================================================================
CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    client_name VARCHAR(255) NOT NULL,
    client_id VARCHAR(255) NOT NULL,
    client_secret TEXT,
    scopes TEXT,
    auth_url TEXT,
    token_url TEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- MCP OAuth Tokens
-- ============================================================================
CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    provider VARCHAR(255) NOT NULL,
    access_token TEXT NOT NULL,
    refresh_token TEXT,
    token_type VARCHAR(50) DEFAULT 'bearer',
    expires_at DATETIME(6) DEFAULT NULL,
    scopes TEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Audit Logs
-- ============================================================================
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) DEFAULT NULL,
    action VARCHAR(255) NOT NULL,
    resource_type VARCHAR(255) NOT NULL,
    resource_id VARCHAR(255) DEFAULT NULL,
    detail JSON DEFAULT NULL,
    ip_address VARCHAR(45) DEFAULT NULL,
    user_agent TEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_audit_logs_tenant ON audit_logs(tenant_id);
CREATE INDEX idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_created ON audit_logs(created_at);
CREATE INDEX idx_audit_logs_resource ON audit_logs(resource_type, resource_id);

-- ============================================================================
-- System Settings
-- ============================================================================
CREATE TABLE IF NOT EXISTS system_settings (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    `key` VARCHAR(128) NOT NULL,
    value JSON NOT NULL,
    value_type VARCHAR(16) NOT NULL,
    category VARCHAR(32) NOT NULL,
    description TEXT NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    requires_restart BOOLEAN NOT NULL DEFAULT FALSE,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_system_settings_key (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_system_settings_category ON system_settings(category);

-- ============================================================================
-- User KB Pins
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_kb_pins (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_user_kb_pin (tenant_id, user_id, kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- User Resource Favorites
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_resource_favorites (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    resource_type VARCHAR(50) NOT NULL,
    resource_id VARCHAR(36) NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_user_resource_fav (tenant_id, user_id, resource_type, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- User Preferences
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_preferences (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    preferences JSON NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_user_prefs (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- KB Shares
-- ============================================================================
CREATE TABLE IF NOT EXISTS kb_shares (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    kb_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    target_tenant_id BIGINT DEFAULT NULL,
    target_org_id BIGINT DEFAULT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_kb_share (kb_id, (COALESCE(target_tenant_id, 0)), (COALESCE(target_org_id, 0)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Knowledge Processing Spans
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    knowledge_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    span_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    started_at DATETIME(6) DEFAULT NULL,
    finished_at DATETIME(6) DEFAULT NULL,
    error_message TEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_kps_knowledge ON knowledge_processing_spans(knowledge_id);

-- ============================================================================
-- Embed Channels
-- ============================================================================
CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSON NOT NULL,
    welcome_message TEXT NOT NULL DEFAULT (''),
    rate_limit_per_minute INT NOT NULL DEFAULT 30,
    rate_limit_per_day INT NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions BOOLEAN NOT NULL DEFAULT TRUE,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search BOOLEAN NOT NULL DEFAULT FALSE,
    allow_memory BOOLEAN NOT NULL DEFAULT FALSE,
    allow_file_upload BOOLEAN NOT NULL DEFAULT FALSE,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) DEFAULT NULL,
    INDEX idx_embed_channels_tenant (tenant_id),
    INDEX idx_embed_channels_agent (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Knowledge Pending Subtasks
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_pending_subtasks (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    knowledge_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(50) NOT NULL,
    payload JSON DEFAULT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    fail_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 3,
    last_error TEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_kps_knowledge_id ON knowledge_pending_subtasks(knowledge_id);

-- ============================================================================
-- Principal Model
-- Note: AUTO_INCREMENT starts at 10000 to match PostgreSQL sequence offset
--       (the unique constraint uk_principal_model uses natural keys for
--       lookups; the id column is an internal surrogate, so the offset has
--       no functional impact — it only avoids overlap with legacy test data).
-- ============================================================================
CREATE TABLE IF NOT EXISTS principal_models (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    principal_id VARCHAR(255) NOT NULL,
    principal_type VARCHAR(50) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_principal_model (tenant_id, principal_id, principal_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=10000;

-- MySQL 8.0.16+ baseline schema for WeKnora metadata layer.
-- Squashed from migrations/versioned/*.up.sql (PostgreSQL, 72 incremental
-- migrations). Fresh MySQL deployments only need this head schema.
--
-- Requires MySQL 8.0.16+ (CHECK constraint enforcement, JSON expression
-- defaults, utf8mb4_0900_ai_ci).
--
-- Scope: metadata layer only. The embeddings table is intentionally absent -
-- under DB_DRIVER=mysql, vector retrieval is delegated to an external engine
-- via RETRIEVE_DRIVER.

SET FOREIGN_KEY_CHECKS = 0;
SET time_zone = '+00:00';

-- tenants
CREATE TABLE tenants (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    retriever_engines JSON NOT NULL DEFAULT (JSON_ARRAY()),
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
    chat_history_config JSON DEFAULT NULL,
    retrieval_config JSON DEFAULT NULL,
    api_principal_config JSON DEFAULT NULL,
    credentials JSON DEFAULT NULL,
    default_storage_backend_id VARCHAR(36) DEFAULT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_tenants_status (status)
) ENGINE=InnoDB AUTO_INCREMENT=10000 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- users
CREATE TABLE users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id BIGINT UNSIGNED,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT FALSE,
    is_system_admin BOOLEAN NOT NULL DEFAULT FALSE,
    preferences JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    CONSTRAINT users_username_key UNIQUE (username),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT fk_users_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL,
    INDEX idx_users_tenant_id (tenant_id),
    INDEX idx_users_deleted_at (deleted_at),
    INDEX idx_users_is_system_admin (is_system_admin)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- auth_tokens
CREATE TABLE auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token TEXT NOT NULL DEFAULT (''),
    token_type VARCHAR(50) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_auth_tokens_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    INDEX idx_auth_tokens_user_id (user_id),
    INDEX idx_auth_tokens_token (token(255)),
    INDEX idx_auth_tokens_token_type (token_type),
    INDEX idx_auth_tokens_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- models
CREATE TABLE models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON NOT NULL DEFAULT (JSON_OBJECT()),
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    managed_by VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_models_type (type),
    INDEX idx_models_source (source),
    INDEX idx_models_is_builtin (is_builtin),
    INDEX idx_models_managed_by_yaml (managed_by)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- knowledge_bases
CREATE TABLE knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    is_temporary BOOLEAN NOT NULL DEFAULT FALSE,
    creator_id VARCHAR(36),
    chunking_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    image_processing_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    vlm_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    extract_config JSON NULL DEFAULT NULL,
    faq_config JSON DEFAULT NULL,
    question_generation_config JSON NULL DEFAULT NULL,
    storage_provider_config JSON DEFAULT NULL,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    asr_config JSON DEFAULT NULL,
    wiki_config JSON DEFAULT NULL,
    indexing_strategy JSON DEFAULT NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(6) NULL DEFAULT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_knowledge_bases_tenant_id (tenant_id),
    INDEX idx_knowledge_bases_tenant_name (tenant_id, name),
    INDEX idx_knowledge_bases_tenant_creator (tenant_id, creator_id),
    INDEX idx_knowledge_bases_tenant_vector_store (tenant_id, vector_store_id),
    INDEX idx_knowledge_bases_storage_backend (tenant_id, storage_backend_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- knowledges
CREATE TABLE knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
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
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result JSON DEFAULT NULL,
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    processed_at DATETIME(6) NULL,
    error_message TEXT,
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_knowledges_tenant_id (tenant_id),
    INDEX idx_knowledges_base_created (knowledge_base_id, created_at),
    INDEX idx_knowledges_parse_status (parse_status),
    INDEX idx_knowledges_enable_status (enable_status),
    INDEX idx_knowledges_summary_status (summary_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- sessions
CREATE TABLE sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    user_id VARCHAR(512),
    title VARCHAR(255),
    description TEXT,
    knowledge_base_id VARCHAR(36),
    agent_id VARCHAR(36),
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT TRUE,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response TEXT NOT NULL DEFAULT (''),
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters JSON NOT NULL DEFAULT (JSON_OBJECT()),
    agent_config JSON DEFAULT NULL,
    context_config JSON DEFAULT NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(6) NULL DEFAULT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_sessions_tenant_updated (tenant_id, updated_at),
    INDEX idx_sessions_agent_id (agent_id),
    INDEX idx_sessions_tenant_user_pin (tenant_id, user_id, is_pinned, pinned_at, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- messages
CREATE TABLE messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content MEDIUMTEXT NOT NULL DEFAULT (''),
    knowledge_references JSON NOT NULL DEFAULT (JSON_ARRAY()),
    agent_steps JSON DEFAULT NULL,
    is_completed BOOLEAN NOT NULL DEFAULT FALSE,
    is_fallback BOOLEAN DEFAULT FALSE,
    mentioned_items JSON DEFAULT (JSON_ARRAY()),
    images JSON DEFAULT (JSON_ARRAY()),
    attachments JSON DEFAULT (JSON_ARRAY()),
    agent_duration_ms BIGINT DEFAULT 0,
    rendered_content TEXT NOT NULL DEFAULT (''),
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context JSON NOT NULL DEFAULT (JSON_OBJECT()),
    knowledge_id VARCHAR(36),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_messages_session_created (session_id, created_at),
    INDEX idx_messages_agent_id (agent_id),
    INDEX idx_messages_knowledge_id (knowledge_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- chunks
CREATE TABLE chunks (
    id VARCHAR(36) PRIMARY KEY,
    seq_id BIGINT UNSIGNED AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content MEDIUMTEXT NOT NULL DEFAULT (''),
    chunk_index INTEGER NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    flags INTEGER NOT NULL DEFAULT 1,
    status INT NOT NULL DEFAULT 0,
    start_at INTEGER NOT NULL,
    end_at INTEGER NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info TEXT,
    video_info TEXT,
    relation_chunks JSON,
    indirect_relation_chunks JSON,
    metadata JSON,
    tag_id VARCHAR(36),
    content_hash VARCHAR(64),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    UNIQUE idx_chunks_seq_id (seq_id),
    INDEX idx_chunks_tenant_kg (tenant_id, knowledge_id),
    INDEX idx_chunks_parent_id (parent_chunk_id),
    INDEX idx_chunks_chunk_type (chunk_type),
    INDEX idx_chunks_tag (tag_id),
    INDEX idx_chunks_content_hash (content_hash),
    INDEX idx_chunks_kb_tenant (knowledge_base_id, tenant_id),
    INDEX idx_chunks_knowledge_enabled (knowledge_id, is_enabled, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- knowledge_tags
CREATE TABLE knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    seq_id BIGINT UNSIGNED AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    UNIQUE idx_knowledge_tags_seq_id (seq_id),
    UNIQUE idx_knowledge_tags_kb_name (tenant_id, knowledge_base_id, name),
    INDEX idx_knowledge_tags_kb (tenant_id, knowledge_base_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- knowledge_tag_relations
CREATE TABLE knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (knowledge_id, tag_id),
    INDEX idx_ktr_knowledge (knowledge_id),
    INDEX idx_ktr_tag (tag_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- mcp_services
CREATE TABLE mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled BOOLEAN DEFAULT TRUE,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512),
    headers JSON,
    auth_config JSON,
    advanced_config JSON,
    stdio_config JSON,
    env_vars JSON,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_mcp_services_tenant_id (tenant_id),
    INDEX idx_mcp_services_enabled (enabled),
    INDEX idx_mcp_services_deleted_at (deleted_at),
    INDEX idx_mcp_services_is_builtin (is_builtin)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- mcp_tool_approvals
CREATE TABLE mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_mcp_tool_approvals_service FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE,
    UNIQUE idx_mcp_tool_approvals_tenant_svc_tool (tenant_id, service_id, tool_name),
    INDEX idx_mcp_tool_approvals_service_id (service_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- mcp_oauth_clients
CREATE TABLE mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    client_id VARCHAR(512) NOT NULL,
    client_secret TEXT,
    redirect_uri VARCHAR(1024),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_mcp_oauth_clients_service FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE,
    UNIQUE idx_mcp_oauth_clients_tenant_svc (tenant_id, service_id),
    INDEX idx_mcp_oauth_clients_service_id (service_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- mcp_oauth_tokens
CREATE TABLE mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    user_id VARCHAR(512) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    principal_type VARCHAR(32) NOT NULL,
    principal_id VARCHAR(512) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32),
    expires_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_mcp_oauth_tokens_service FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE,
    UNIQUE idx_mcp_oauth_tokens_tenant_principal_svc (tenant_id, principal_type, principal_id, service_id),
    INDEX idx_mcp_oauth_tokens_service_id (service_id),
    INDEX idx_mcp_oauth_tokens_user_id (user_id),
    INDEX idx_mcp_oauth_tokens_principal (principal_type, principal_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- custom_agents
CREATE TABLE custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    tenant_id BIGINT UNSIGNED NOT NULL,
    created_by VARCHAR(36),
    runnable_by_viewer BOOLEAN NOT NULL DEFAULT TRUE,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    PRIMARY KEY (id, tenant_id),
    INDEX idx_custom_agents_tenant_id (tenant_id),
    INDEX idx_custom_agents_is_builtin (is_builtin),
    INDEX idx_custom_agents_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- organizations
CREATE TABLE organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id BIGINT UNSIGNED NOT NULL,
    invite_code VARCHAR(32),
    require_approval BOOLEAN DEFAULT FALSE,
    invite_code_expires_at DATETIME(6) NULL,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT FALSE,
    member_limit INTEGER NOT NULL DEFAULT 50,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    live_invite_code VARCHAR(32) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN invite_code ELSE NULL END) VIRTUAL,
    UNIQUE idx_organizations_live_invite_code (live_invite_code),
    INDEX idx_organizations_owner_id (owner_id),
    INDEX idx_organizations_owner_tenant (owner_tenant_id),
    INDEX idx_organizations_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- organization_join_requests
CREATE TABLE organization_join_requests (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32),
    message TEXT,
    reviewed_by VARCHAR(36),
    reviewed_at DATETIME(6) NULL,
    review_message TEXT,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_org_join_requests_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
    pending_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN status = 'pending' THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE uq_org_join_requests_pending_live (organization_id, tenant_id, request_type, pending_marker),
    INDEX idx_org_join_requests_org_id (organization_id),
    INDEX idx_org_join_requests_user_id (user_id),
    INDEX idx_org_join_requests_status (status),
    INDEX idx_org_join_requests_type (request_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- organization_tenant_members
CREATE TABLE organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_org_tenant_members_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
    UNIQUE idx_org_tenant_members_unique (organization_id, tenant_id),
    INDEX idx_org_tenant_members_by_tenant (tenant_id),
    INDEX idx_org_tenant_members_role (organization_id, role)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- kb_shares
CREATE TABLE kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT UNSIGNED NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    CONSTRAINT fk_kb_shares_kb FOREIGN KEY (knowledge_base_id) REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    CONSTRAINT fk_kb_shares_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
    INDEX idx_kb_shares_kb_id (knowledge_base_id),
    INDEX idx_kb_shares_org_id (organization_id),
    INDEX idx_kb_shares_source_tenant (source_tenant_id),
    INDEX idx_kb_shares_deleted_at (deleted_at),
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_kb_shares_live (knowledge_base_id, organization_id, live_marker)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- agent_shares
CREATE TABLE agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT UNSIGNED NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    CONSTRAINT fk_agent_shares_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_shares_agent FOREIGN KEY (agent_id, source_tenant_id) REFERENCES custom_agents(id, tenant_id) ON DELETE CASCADE,
    INDEX idx_agent_shares_agent_id (agent_id),
    INDEX idx_agent_shares_org_id (organization_id),
    INDEX idx_agent_shares_source_tenant (source_tenant_id),
    INDEX idx_agent_shares_deleted_at (deleted_at),
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_agent_shares_live (agent_id, source_tenant_id, organization_id, live_marker)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- tenant_disabled_shared_agents
CREATE TABLE tenant_disabled_shared_agents (
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id),
    INDEX idx_tenant_disabled_shared_agents_tenant_id (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- im_channels
CREATE TABLE im_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    credentials JSON NOT NULL DEFAULT (JSON_OBJECT()),
    knowledge_base_id VARCHAR(36) DEFAULT '',
    bot_identity VARCHAR(255) NOT NULL DEFAULT '',
    session_mode VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    CONSTRAINT chk_im_channels_session_mode CHECK (session_mode IN ('user', 'thread')),
    INDEX idx_im_channels_tenant (tenant_id),
    INDEX idx_im_channels_agent (agent_id),
    INDEX idx_im_channels_deleted (deleted_at),
    live_bot_identity VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND bot_identity <> '' THEN bot_identity ELSE NULL END) VIRTUAL,
    UNIQUE idx_im_channels_live_bot_identity (live_bot_identity)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- im_channel_sessions
CREATE TABLE im_channel_sessions (
    id VARCHAR(36) PRIMARY KEY,
    platform VARCHAR(20) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    chat_id VARCHAR(128) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) DEFAULT '',
    im_channel_id VARCHAR(36) DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    thread_id VARCHAR(128) NOT NULL DEFAULT '',
    metadata JSON DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    CONSTRAINT fk_im_channel_sessions_session FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    INDEX idx_im_channel_tenant (tenant_id),
    INDEX idx_im_channel_session (session_id),
    INDEX idx_im_channel_deleted (deleted_at),
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    live_thread_key VARCHAR(512) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND thread_id <> '' THEN CONCAT(platform, '\|', tenant_id, '\|', agent_id, '\|', chat_id, '\|', thread_id) ELSE NULL END) VIRTUAL,
    UNIQUE idx_im_channel_sessions_live_channel (platform, user_id, chat_id, tenant_id, agent_id, live_marker),
    UNIQUE idx_im_channel_sessions_live_thread (live_thread_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- data_sources
CREATE TABLE data_sources (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config JSON,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT 'incremental',
    status VARCHAR(32) DEFAULT 'active',
    conflict_strategy VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions BOOLEAN DEFAULT TRUE,
    last_sync_at DATETIME(6) NULL,
    last_sync_cursor JSON,
    last_sync_result JSON,
    error_message TEXT,
    sync_log_retention_days INT DEFAULT 30,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_data_sources_tenant_id (tenant_id),
    INDEX idx_data_sources_knowledge_base_id (knowledge_base_id),
    INDEX idx_data_sources_type (type),
    INDEX idx_data_sources_status (status),
    INDEX idx_data_sources_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- sync_logs
CREATE TABLE sync_logs (
    id VARCHAR(36) PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6) NULL,
    items_total INT DEFAULT 0,
    items_created INT DEFAULT 0,
    items_updated INT DEFAULT 0,
    items_deleted INT DEFAULT 0,
    items_skipped INT DEFAULT 0,
    items_failed INT DEFAULT 0,
    error_message TEXT,
    result JSON,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_sync_logs_data_source FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE,
    INDEX idx_sync_logs_ds_started (data_source_id, started_at),
    INDEX idx_sync_logs_tenant_id (tenant_id),
    INDEX idx_sync_logs_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- web_search_providers
CREATE TABLE web_search_providers (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON,
    is_default BOOLEAN DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_web_search_providers_tenant_id (tenant_id),
    INDEX idx_web_search_providers_provider (provider),
    INDEX idx_web_search_providers_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- vector_stores
CREATE TABLE vector_stores (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    index_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    tenant_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_vector_stores_name_tenant_live (name, tenant_id, live_marker),
    INDEX idx_vector_stores_tenant_id (tenant_id),
    INDEX idx_vector_stores_engine_type (engine_type),
    INDEX idx_vector_stores_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- embed_channels
CREATE TABLE embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSON NOT NULL DEFAULT (JSON_ARRAY()),
    welcome_message TEXT NOT NULL DEFAULT (''),
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    rate_limit_per_day INTEGER NOT NULL DEFAULT 10000,
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
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_embed_channels_tenant (tenant_id),
    INDEX idx_embed_channels_agent (agent_id),
    live_publish_token VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND publish_token <> '' THEN publish_token ELSE NULL END) VIRTUAL,
    UNIQUE idx_embed_channels_live_publish_token (live_publish_token),
    INDEX idx_embed_channels_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- wiki_folders
CREATE TABLE wiki_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_wiki_folders_parent_name_live (knowledge_base_id, parent_id, name, live_marker),
    INDEX idx_wiki_folders_parent (knowledge_base_id, parent_id),
    INDEX idx_wiki_folders_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- wiki_pages
CREATE TABLE wiki_pages (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    title VARCHAR(512) NOT NULL DEFAULT '',
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary',
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    content TEXT NOT NULL DEFAULT (''),
    summary TEXT NOT NULL DEFAULT (''),
    parent_slug VARCHAR(255) NOT NULL DEFAULT '',
    folder_id VARCHAR(36) NOT NULL DEFAULT '',
    category_path JSON,
    wiki_path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    source_refs JSON,
    chunk_refs JSON,
    in_links JSON,
    out_links JSON,
    page_metadata JSON,
    aliases JSON,
    version INT NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_wiki_pages_kb_slug_live (knowledge_base_id, slug, live_marker),
    INDEX idx_wiki_pages_kb_id (knowledge_base_id),
    INDEX idx_wiki_pages_page_type (knowledge_base_id, page_type),
    INDEX idx_wiki_pages_parent_slug (knowledge_base_id, parent_slug),
    -- MySQL InnoDB limits index keys to 3072 bytes. wiki_path (VARCHAR(1024))
    -- and title (VARCHAR(512)) under utf8mb4 would exceed that, so both get
    -- indexed with a prefix. The tree query uses equality on knowledge_base_id
    -- + page_type + wiki_path prefix, then ORDER BY sort_order (INT, full).
    INDEX idx_wiki_pages_tree (knowledge_base_id, page_type, wiki_path(480), sort_order, title(200)),
    INDEX idx_wiki_pages_folder (knowledge_base_id, folder_id),
    INDEX idx_wiki_pages_tenant_id (tenant_id),
    INDEX idx_wiki_pages_deleted_at (deleted_at)
    -- NOTE(perf): PG had 4 GIN indexes on this table (to_tsvector full-
    -- text, source_refs jsonb_path_ops containment, source_refs::text
    -- fulltext, lower(title) gin_trgm_ops similarity). MySQL has no direct
    -- equivalents, so wiki search/ListBySourceRef/FindSimilarPages use
    -- multi-column LIKE + JSON_CONTAINS instead.
    --
    -- This is NOT a simple degradation — in several ways MySQL is more
    -- permissive than PG:
    --   - LIKE '%query%' does SUBSTRING matching (matches 'config' inside
    --     'configuration'), whereas PG's to_tsvector does WORD-level
    --     matching (does not). Switching MySQL to FULLTEXT + MATCH...
    --     AGAINST would REGRESS search recall. Do NOT do this without
    --     WITH PARSER ngram or a LIKE fallback.
    --   - All source_refs queries are scoped by knowledge_base_id (leading
    --     predicate, backed by idx_wiki_pages_kb_id), so JSON_CONTAINS
    --     evaluates within one KB's rows, not a full table scan.
    --
    -- If profiling at production scale reveals a hotspot, consider:
    --   1. FULLTEXT INDEX with WITH PARSER ngram (preserves substring/CJK
    --      matching) + server config innodb_ft_min_token_size=1.
    --   2. MULTI_VALUED INDEX on source_refs (MySQL 8.0.17+) — but only
    --      helps the JSON_CONTAINS branch, not the legacy ::text LIKE
    --      branch, and tightens the version gate.
    -- Measure first; optimize only if profiling shows a hotspot.
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- wiki_page_issues
CREATE TABLE wiki_page_issues (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL DEFAULT (''),
    suspected_knowledge_ids JSON,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    reported_by VARCHAR(100) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_wiki_page_issues_tenant_id (tenant_id),
    INDEX idx_wiki_page_issues_kb_created (knowledge_base_id, created_at),
    INDEX idx_wiki_page_issues_slug (slug),
    INDEX idx_wiki_page_issues_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- wiki_log_entries
CREATE TABLE wiki_log_entries (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action VARCHAR(32) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    doc_title TEXT NOT NULL DEFAULT (''),
    summary TEXT NOT NULL DEFAULT (''),
    pages_affected JSON NOT NULL DEFAULT (JSON_ARRAY()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_wiki_log_entries_kb_id_desc (knowledge_base_id, id DESC),
    INDEX idx_wiki_log_entries_tenant_id (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- task_pending_ops
CREATE TABLE task_pending_ops (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload JSON NOT NULL DEFAULT (JSON_OBJECT()),
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    claimed_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_task_pending_ops_scope (task_type, scope, scope_id, id),
    INDEX idx_task_pending_ops_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- task_dead_letters
CREATE TABLE task_dead_letters (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    payload JSON NOT NULL DEFAULT (JSON_OBJECT()),
    last_error TEXT NOT NULL DEFAULT (''),
    fail_count INT NOT NULL,
    failed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_task_dead_letters_scope (scope, scope_id, failed_at DESC),
    INDEX idx_task_dead_letters_tenant (tenant_id, failed_at DESC),
    INDEX idx_task_dead_letters_task_type (task_type, failed_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- tenant_members
CREATE TABLE tenant_members (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36),
    joined_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_tenant_members_user_tenant_live (user_id, tenant_id, live_marker),
    INDEX idx_tenant_members_tenant_role (tenant_id, role),
    INDEX idx_tenant_members_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- audit_logs
CREATE TABLE audit_logs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    actor_user_id VARCHAR(36) NOT NULL DEFAULT '',
    actor_role VARCHAR(32) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL DEFAULT '',
    target_id VARCHAR(64) NOT NULL DEFAULT '',
    target_user_id VARCHAR(36) NOT NULL DEFAULT '',
    request_path VARCHAR(512) NOT NULL DEFAULT '',
    request_method VARCHAR(16) NOT NULL DEFAULT '',
    outcome VARCHAR(16) NOT NULL DEFAULT 'success',
    details JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_audit_logs_tenant_id_desc (tenant_id, id DESC),
    INDEX idx_audit_logs_actor (actor_user_id),
    INDEX idx_audit_logs_tenant_action (tenant_id, action),
    INDEX idx_audit_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- user_resource_favorites
CREATE TABLE user_resource_favorites (
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id),
    INDEX idx_user_resource_favorites_user_tenant_type_created_at (user_id, tenant_id, resource_type, created_at DESC),
    INDEX idx_user_resource_favorites_tenant_id (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- tenant_invitations
CREATE TABLE tenant_invitations (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL DEFAULT '',
    invited_by VARCHAR(36),
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    message VARCHAR(500),
    token VARCHAR(64) NOT NULL DEFAULT '',
    accepted_count INTEGER NOT NULL DEFAULT 0,
    expires_at DATETIME(6) NOT NULL,
    responded_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    pending_invitee VARCHAR(36) GENERATED ALWAYS AS (CASE WHEN status = 'pending' AND deleted_at IS NULL AND invitee_user_id <> '' THEN invitee_user_id ELSE NULL END) VIRTUAL,
    UNIQUE idx_tenant_invitations_live_pending (tenant_id, pending_invitee),
    live_token VARCHAR(128) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND token <> '' THEN token ELSE NULL END) VIRTUAL,
    UNIQUE idx_tenant_invitations_live_token (live_token),
    INDEX idx_tenant_invitations_tenant (tenant_id),
    INDEX idx_tenant_invitations_invitee (invitee_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- user_kb_pins
CREATE TABLE user_kb_pins (
    tenant_id BIGINT UNSIGNED NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, user_id, kb_id),
    INDEX idx_user_kb_pins_user_tenant_pinned_at (tenant_id, user_id, pinned_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- system_settings
CREATE TABLE system_settings (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    `key` VARCHAR(128) NOT NULL,
    value JSON NOT NULL DEFAULT (JSON_OBJECT()),
    value_type VARCHAR(16) NOT NULL,
    category VARCHAR(32) NOT NULL,
    description TEXT NOT NULL DEFAULT (''),
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    requires_restart BOOLEAN NOT NULL DEFAULT FALSE,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE idx_system_settings_key (`key`),
    INDEX idx_system_settings_category (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- knowledge_processing_spans
CREATE TABLE knowledge_processing_spans (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    knowledge_id VARCHAR(64) NOT NULL,
    attempt INT NOT NULL DEFAULT 1,
    span_id VARCHAR(64) NOT NULL,
    parent_span_id VARCHAR(64),
    name VARCHAR(255) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    input JSON,
    output JSON,
    metadata JSON,
    error_code VARCHAR(64),
    error_message TEXT,
    error_detail TEXT,
    started_at DATETIME(6) NULL,
    finished_at DATETIME(6) NULL,
    duration_ms BIGINT,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id),
    INDEX idx_kpspan_knowledge_attempt (knowledge_id, attempt),
    INDEX idx_kpspan_status_started (status, started_at),
    INDEX idx_kpspan_parent (parent_span_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- message_suggestion_sets
CREATE TABLE message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    allow_regenerate BOOLEAN NOT NULL DEFAULT FALSE,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions JSON NOT NULL DEFAULT (JSON_ARRAY()),
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until DATETIME(6) NULL,
    generated_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_message_suggestion_sets_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    UNIQUE idx_message_suggestion_sets_cache_key (tenant_id, assistant_message_id, placement, config_hash, locale),
    INDEX idx_message_suggestion_sets_session (tenant_id, session_id, created_at),
    INDEX idx_message_suggestion_sets_status (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- message_suggestion_events
CREATE TABLE message_suggestion_events (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_message_suggestion_events_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    CONSTRAINT fk_message_suggestion_events_set FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE,
    INDEX idx_message_suggestion_events_set (suggestion_set_id, created_at),
    INDEX idx_message_suggestion_events_session (tenant_id, session_id, created_at),
    INDEX idx_message_suggestion_events_type (event_type, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- storage_backends
CREATE TABLE storage_backends (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_storage_backends_name_live (tenant_id, name, live_marker),
    live_legacy_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND legacy_alias = TRUE THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_storage_backends_legacy_live (tenant_id, provider, live_legacy_marker),
    INDEX idx_storage_backends_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- resources
CREATE TABLE resources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    handle VARCHAR(22) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    storage_backend_id VARCHAR(36),
    provider VARCHAR(32) NOT NULL,
    physical_path TEXT NOT NULL DEFAULT (''),
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'file',
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    original_name VARCHAR(1024) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    lifecycle VARCHAR(16) NOT NULL DEFAULT 'persistent',
    expires_at DATETIME(6) NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    UNIQUE idx_resources_handle (handle),
    live_marker CHAR(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END) VIRTUAL,
    UNIQUE idx_resources_location_live (tenant_id, location_hash, live_marker),
    INDEX idx_resources_tenant (tenant_id),
    INDEX idx_resources_backend (storage_backend_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- resource_bindings
CREATE TABLE resource_bindings (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_resource_bindings_resource FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE,
    UNIQUE idx_resource_bindings_unique (resource_id, owner_type, owner_id, relation),
    INDEX idx_resource_bindings_owner (tenant_id, owner_type, owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- resource_access_grants
CREATE TABLE resource_access_grants (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL,
    resource_id VARCHAR(36) NOT NULL,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_resource_access_grants_resource FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE,
    UNIQUE idx_resource_access_grants_token_hash (token_hash),
    INDEX idx_resource_access_grants_resource (resource_id),
    INDEX idx_resource_access_grants_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- tenant_api_keys
CREATE TABLE tenant_api_keys (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NULL,
    name VARCHAR(128) NOT NULL,
    key_hash VARCHAR(64) NOT NULL,
    api_key TEXT NOT NULL DEFAULT (''),
    scope_type VARCHAR(16) NOT NULL DEFAULT 'tenant',
    full_access BOOLEAN NOT NULL DEFAULT FALSE,
    knowledge_base_ids JSON NOT NULL DEFAULT (JSON_ARRAY()),
    capabilities JSON NOT NULL DEFAULT (JSON_ARRAY()),
    last_used_at DATETIME(6) NULL,
    expires_at DATETIME(6) NULL,
    revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_tenant_api_keys_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    CONSTRAINT chk_tenant_api_keys_scope CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = FALSE)
    ),
    UNIQUE idx_tenant_api_keys_key_hash (key_hash),
    INDEX idx_tenant_api_keys_tenant (tenant_id),
    INDEX idx_tenant_api_keys_scope_type (scope_type),
    INDEX idx_tenant_api_keys_revoked_at (revoked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- temporary_documents
CREATE TABLE temporary_documents (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref TEXT NOT NULL DEFAULT (''),
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content TEXT NOT NULL DEFAULT (''),
    chunks JSON NOT NULL DEFAULT (JSON_ARRAY()),
    image_refs JSON NOT NULL DEFAULT (JSON_ARRAY()),
    metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    processing_options JSON NOT NULL DEFAULT (JSON_OBJECT()),
    token_count INTEGER NOT NULL DEFAULT 0,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT (''),
    expires_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    ready_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL DEFAULT NULL,
    INDEX idx_temporary_documents_scope (tenant_id, session_id),
    INDEX idx_temporary_documents_status (status),
    INDEX idx_temporary_documents_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

SET FOREIGN_KEY_CHECKS = 1;

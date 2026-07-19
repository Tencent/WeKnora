-- WeKnora MySQL business-primary baseline.
--
-- This schema deliberately contains only the transactional/business database.
-- Vector data is owned by the configured external retrieval backend
-- (for example Qdrant) and must not be added here.
--
-- The baseline represents the business schema after the PostgreSQL migrations
-- through 000070. Future MySQL and PostgreSQL migrations should advance from
-- 000071 together.

SET NAMES utf8mb4;
SET time_zone = '+00:00';

CREATE TABLE IF NOT EXISTS tenants (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    retriever_engines JSON NOT NULL DEFAULT (JSON_ARRAY()),
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    agent_config JSON NULL,
    context_config JSON NULL,
    conversation_config JSON NULL,
    web_search_config JSON NULL,
    parser_engine_config JSON NULL,
    storage_engine_config JSON NULL,
    default_storage_backend_id VARCHAR(36) NULL,
    credentials JSON NULL,
    chat_history_config JSON NULL,
    retrieval_config JSON NULL,
    api_principal_config JSON NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_tenants_status (status),
    KEY idx_tenants_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci
  AUTO_INCREMENT = 10000;

CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) NOT NULL,
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
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_models_tenant_id (tenant_id),
    KEY idx_models_type (type),
    KEY idx_models_source (source),
    KEY idx_models_is_builtin (is_builtin),
    KEY idx_models_managed_by (managed_by),
    KEY idx_models_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    image_processing_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    storage_provider_config JSON NULL,
    vlm_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    extract_config JSON NULL,
    faq_config JSON NULL,
    question_generation_config JSON NULL,
    wiki_config JSON NULL,
    indexing_strategy JSON NULL,
    is_temporary BOOLEAN NOT NULL DEFAULT FALSE,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(6) NULL,
    asr_config JSON NULL,
    vector_store_id VARCHAR(36) NULL,
    storage_backend_id VARCHAR(36) NULL,
    creator_id VARCHAR(36) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_knowledge_bases_tenant_id (tenant_id),
    KEY idx_knowledge_bases_tenant_vector_store (tenant_id, vector_store_id),
    KEY idx_knowledge_bases_storage_backend (tenant_id, storage_backend_id),
    KEY idx_knowledge_bases_tenant_creator (tenant_id, creator_id),
    KEY idx_knowledge_bases_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    source VARCHAR(2048) NOT NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64) NULL,
    file_name VARCHAR(255) NULL,
    file_type VARCHAR(50) NULL,
    file_size BIGINT NULL,
    file_path TEXT,
    file_hash VARCHAR(64) NULL,
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata JSON NULL,
    summary_status VARCHAR(32) NOT NULL DEFAULT 'none',
    last_faq_import_result JSON NULL,
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    processed_at DATETIME(6) NULL,
    error_message TEXT,
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_knowledges_tenant_id (tenant_id),
    KEY idx_knowledges_base_id (knowledge_base_id),
    KEY idx_knowledges_parse_status (parse_status),
    KEY idx_knowledges_enable_status (enable_status),
    KEY idx_knowledges_summary_status (summary_status),
    KEY idx_knowledges_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(255) NULL,
    description TEXT,
    knowledge_base_id VARCHAR(36) NULL,
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT TRUE,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response TEXT NOT NULL,
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64) NULL,
    embedding_top_k INT NOT NULL DEFAULT 10,
    rerank_top_k INT NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64) NULL,
    summary_parameters JSON NOT NULL DEFAULT (JSON_OBJECT()),
    agent_config JSON NULL,
    context_config JSON NULL,
    agent_id VARCHAR(36) NULL,
    user_id VARCHAR(512) NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_sessions_tenant_id (tenant_id),
    KEY idx_sessions_agent_id (agent_id),
    KEY idx_sessions_tenant_user_pin
        (tenant_id, user_id, is_pinned, pinned_at, updated_at),
    KEY idx_sessions_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) NOT NULL,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    rendered_content TEXT NOT NULL,
    knowledge_references JSON NOT NULL DEFAULT (JSON_ARRAY()),
    agent_steps JSON NULL,
    mentioned_items JSON NOT NULL DEFAULT (JSON_ARRAY()),
    images JSON NOT NULL DEFAULT (JSON_ARRAY()),
    attachments JSON NOT NULL DEFAULT (JSON_ARRAY()),
    is_completed BOOLEAN NOT NULL DEFAULT FALSE,
    is_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context JSON NOT NULL DEFAULT (JSON_OBJECT()),
    agent_duration_ms BIGINT NOT NULL DEFAULT 0,
    knowledge_id VARCHAR(36) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_messages_session_id (session_id),
    KEY idx_messages_knowledge_id (knowledge_id),
    KEY idx_messages_agent_id (agent_id),
    KEY idx_messages_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) NOT NULL,
    seq_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    chunk_index INT NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    start_at INT NOT NULL,
    end_at INT NOT NULL,
    pre_chunk_id VARCHAR(36) NULL,
    next_chunk_id VARCHAR(36) NULL,
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36) NULL,
    image_info TEXT,
    video_info TEXT,
    relation_chunks JSON NULL,
    indirect_relation_chunks JSON NULL,
    metadata JSON NULL,
    tag_id VARCHAR(36) NULL,
    status INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NULL,
    flags INT NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_chunks_seq_id (seq_id),
    KEY idx_chunks_tenant_knowledge (tenant_id, knowledge_id),
    KEY idx_chunks_kb_tenant (knowledge_base_id, tenant_id),
    KEY idx_chunks_knowledge_enabled (knowledge_id, is_enabled, deleted_at),
    KEY idx_chunks_parent_id (parent_chunk_id),
    KEY idx_chunks_chunk_type (chunk_type),
    KEY idx_chunks_tag (tag_id),
    KEY idx_chunks_content_hash (content_hash),
    KEY idx_chunks_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) NOT NULL,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500) NULL,
    tenant_id BIGINT UNSIGNED NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT FALSE,
    is_system_admin BOOLEAN NOT NULL DEFAULT FALSE,
    preferences JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_users_username (username),
    UNIQUE KEY uq_users_email (email),
    KEY idx_users_tenant_id (tenant_id),
    KEY idx_users_is_system_admin (is_system_admin),
    KEY idx_users_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS auth_tokens (
    id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    token TEXT NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_auth_tokens_user_id (user_id),
    KEY idx_auth_tokens_token (token(191)),
    KEY idx_auth_tokens_token_type (token_type),
    KEY idx_auth_tokens_expires_at (expires_at),
    CONSTRAINT fk_auth_tokens_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_members (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36) NULL,
    joined_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_marker TINYINT
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, 1, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uq_tenant_members_user_tenant (user_id, tenant_id, live_marker),
    KEY idx_tenant_members_tenant_role (tenant_id, role, deleted_at),
    KEY idx_tenant_members_user (user_id, deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
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
    PRIMARY KEY (id),
    KEY idx_audit_logs_tenant_id_desc (tenant_id, id DESC),
    KEY idx_audit_logs_actor (actor_user_id),
    KEY idx_audit_logs_tenant_action (tenant_id, action),
    KEY idx_audit_logs_created_at (created_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS user_resource_favorites (
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id),
    KEY idx_user_resource_favorites_user_tenant_type_created_at
        (user_id, tenant_id, resource_type, created_at DESC),
    KEY idx_user_resource_favorites_tenant_id (tenant_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS user_kb_pins (
    tenant_id BIGINT UNSIGNED NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, user_id, kb_id),
    KEY idx_user_kb_pins_user_tenant_pinned_at
        (tenant_id, user_id, pinned_at DESC)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_invitations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL DEFAULT '',
    invited_by VARCHAR(36) NULL,
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    message VARCHAR(500) NULL,
    expires_at DATETIME(6) NOT NULL,
    responded_at DATETIME(6) NULL,
    token VARCHAR(64) NOT NULL DEFAULT '',
    accepted_count INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    pending_invitee_key VARCHAR(36)
        GENERATED ALWAYS AS (
            IF(status = 'pending' AND deleted_at IS NULL AND invitee_user_id <> '',
               invitee_user_id, NULL)
        ) STORED,
    pending_token_key VARCHAR(64)
        GENERATED ALWAYS AS (
            IF(deleted_at IS NULL AND token <> '', token, NULL)
        ) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uq_tenant_invitations_pending_invitee
        (tenant_id, pending_invitee_key),
    UNIQUE KEY uq_tenant_invitations_pending_token (pending_token_key),
    KEY idx_tenant_invitations_tenant (tenant_id, deleted_at),
    KEY idx_tenant_invitations_invitee (invitee_user_id, deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) NOT NULL,
    seq_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32) NULL,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_knowledge_tags_seq_id (seq_id),
    UNIQUE KEY idx_knowledge_tags_kb_name (tenant_id, knowledge_base_id, name),
    KEY idx_knowledge_tags_kb (tenant_id, knowledge_base_id),
    KEY idx_knowledge_tags_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mcp_services (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512) NULL,
    headers JSON NULL,
    auth_config JSON NULL,
    advanced_config JSON NULL,
    stdio_config JSON NULL,
    env_vars JSON NULL,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_mcp_services_tenant_id (tenant_id),
    KEY idx_mcp_services_enabled (enabled),
    KEY idx_mcp_services_is_builtin (is_builtin),
    KEY idx_mcp_services_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY idx_mcp_tool_approvals_tenant_svc_tool
        (tenant_id, service_id, tool_name),
    KEY idx_mcp_tool_approvals_service_id (service_id),
    CONSTRAINT fk_mcp_tool_approvals_service
        FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64) NULL,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_by VARCHAR(36) NULL,
    runnable_by_viewer BOOLEAN NOT NULL DEFAULT TRUE,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id, tenant_id),
    KEY idx_custom_agents_tenant_id (tenant_id),
    KEY idx_custom_agents_is_builtin (is_builtin),
    KEY idx_custom_agents_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    invite_code VARCHAR(32) NULL,
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    invite_code_expires_at DATETIME(6) NULL,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) NOT NULL DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT FALSE,
    member_limit INT NOT NULL DEFAULT 50,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    invite_code_key VARCHAR(32)
        GENERATED ALWAYS AS (
            IF(deleted_at IS NULL AND invite_code IS NOT NULL AND invite_code <> '',
               invite_code, NULL)
        ) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_organizations_invite_code (invite_code_key),
    KEY idx_organizations_owner_id (owner_id),
    KEY idx_organizations_owner_tenant (owner_tenant_id),
    KEY idx_organizations_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY idx_org_tenant_members_unique (organization_id, tenant_id),
    KEY idx_org_tenant_members_by_tenant (tenant_id),
    KEY idx_org_tenant_members_role (organization_id, role),
    CONSTRAINT fk_org_tenant_members_org
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS kb_shares (
    id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT UNSIGNED NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_marker TINYINT
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, 1, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_kb_shares_kb_org (knowledge_base_id, organization_id, live_marker),
    KEY idx_kb_shares_kb_id (knowledge_base_id),
    KEY idx_kb_shares_org_id (organization_id),
    KEY idx_kb_shares_source_tenant (source_tenant_id),
    KEY idx_kb_shares_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS organization_join_requests (
    id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32) NULL,
    message TEXT,
    reviewed_by VARCHAR(36) NULL,
    reviewed_at DATETIME(6) NULL,
    review_message TEXT,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    pending_marker TINYINT
        GENERATED ALWAYS AS (IF(status = 'pending', 1, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uq_org_join_requests_pending_per_tenant
        (organization_id, tenant_id, request_type, pending_marker),
    KEY idx_org_join_requests_org_id (organization_id),
    KEY idx_org_join_requests_user_id (user_id),
    KEY idx_org_join_requests_status (status),
    KEY idx_org_join_requests_type (request_type)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS agent_shares (
    id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT UNSIGNED NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_marker TINYINT
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, 1, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_agent_shares_agent_org
        (agent_id, source_tenant_id, organization_id, live_marker),
    KEY idx_agent_shares_agent_id (agent_id),
    KEY idx_agent_shares_org_id (organization_id),
    KEY idx_agent_shares_source_tenant (source_tenant_id),
    KEY idx_agent_shares_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id),
    KEY idx_tenant_disabled_shared_agents_tenant_id (tenant_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS im_channels (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    bot_identity VARCHAR(255) NOT NULL DEFAULT '',
    session_mode VARCHAR(20) NOT NULL DEFAULT 'user',
    credentials JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    bot_identity_key VARCHAR(255)
        GENERATED ALWAYS AS (
            IF(deleted_at IS NULL AND bot_identity <> '', bot_identity, NULL)
        ) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_im_channels_bot_identity (bot_identity_key),
    KEY idx_im_channels_tenant (tenant_id),
    KEY idx_im_channels_agent (agent_id),
    KEY idx_im_channels_deleted (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    chat_id VARCHAR(128) NOT NULL DEFAULT '',
    thread_id VARCHAR(128) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    im_channel_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    user_live_marker TINYINT
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, 1, NULL)) STORED,
    thread_key VARCHAR(128)
        GENERATED ALWAYS AS (
            IF(deleted_at IS NULL AND thread_id <> '', thread_id, NULL)
        ) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_channel_lookup
        (platform, user_id, chat_id, tenant_id, agent_id, user_live_marker),
    UNIQUE KEY idx_channel_thread_lookup
        (platform, chat_id, thread_key, tenant_id, agent_id),
    KEY idx_im_channel_tenant (tenant_id),
    KEY idx_im_channel_session (session_id),
    KEY idx_im_channel_sessions_channel (im_channel_id),
    KEY idx_im_channel_deleted (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSON NOT NULL DEFAULT (JSON_ARRAY()),
    welcome_message TEXT NOT NULL,
    rate_limit_per_minute INT NOT NULL DEFAULT 30,
    rate_limit_per_day INT NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions BOOLEAN NOT NULL DEFAULT TRUE,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search BOOLEAN NOT NULL DEFAULT FALSE,
    allow_file_upload BOOLEAN NOT NULL DEFAULT FALSE,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    publish_token_key VARCHAR(64)
        GENERATED ALWAYS AS (
            IF(deleted_at IS NULL AND publish_token <> '', publish_token, NULL)
        ) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_embed_channels_publish_token (publish_token_key),
    KEY idx_embed_channels_tenant (tenant_id),
    KEY idx_embed_channels_agent (agent_id),
    KEY idx_embed_channels_deleted (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config JSON NULL,
    sync_schedule VARCHAR(100) NULL,
    sync_mode VARCHAR(20) NOT NULL DEFAULT 'incremental',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    conflict_strategy VARCHAR(32) NOT NULL DEFAULT 'overwrite',
    sync_deletions BOOLEAN NOT NULL DEFAULT TRUE,
    last_sync_at DATETIME(6) NULL,
    last_sync_cursor JSON NULL,
    last_sync_result JSON NULL,
    error_message TEXT,
    sync_log_retention_days INT NOT NULL DEFAULT 30,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_data_sources_tenant_id (tenant_id),
    KEY idx_data_sources_knowledge_base_id (knowledge_base_id),
    KEY idx_data_sources_type (type),
    KEY idx_data_sources_status (status),
    KEY idx_data_sources_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_logs (
    id VARCHAR(36) NOT NULL,
    data_source_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6) NULL,
    items_total INT NOT NULL DEFAULT 0,
    items_created INT NOT NULL DEFAULT 0,
    items_updated INT NOT NULL DEFAULT 0,
    items_deleted INT NOT NULL DEFAULT 0,
    items_skipped INT NOT NULL DEFAULT 0,
    items_failed INT NOT NULL DEFAULT 0,
    error_message TEXT,
    result JSON NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_sync_logs_data_source_id (data_source_id),
    KEY idx_sync_logs_tenant_id (tenant_id),
    KEY idx_sync_logs_status (status),
    KEY idx_sync_logs_started_at (started_at),
    CONSTRAINT fk_sync_logs_data_source
        FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS web_search_providers (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_web_search_providers_tenant_id (tenant_id),
    KEY idx_web_search_providers_provider (provider),
    KEY idx_web_search_providers_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS vector_stores (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    index_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    tenant_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_name VARCHAR(255)
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_vector_stores_name_tenant (tenant_id, live_name),
    KEY idx_vector_stores_tenant_id (tenant_id),
    KEY idx_vector_stores_engine_type (engine_type),
    KEY idx_vector_stores_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS wiki_pages (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    title VARCHAR(512) NOT NULL DEFAULT '',
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary',
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    content TEXT NOT NULL,
    summary TEXT NOT NULL,
    aliases JSON NOT NULL DEFAULT (JSON_ARRAY()),
    parent_slug VARCHAR(255) NOT NULL DEFAULT '',
    folder_id VARCHAR(36) NOT NULL DEFAULT '',
    category_path JSON NOT NULL DEFAULT (JSON_ARRAY()),
    wiki_path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    source_refs JSON NOT NULL DEFAULT (JSON_ARRAY()),
    chunk_refs JSON NOT NULL DEFAULT (JSON_ARRAY()),
    in_links JSON NOT NULL DEFAULT (JSON_ARRAY()),
    out_links JSON NOT NULL DEFAULT (JSON_ARRAY()),
    page_metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    version INT NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_slug VARCHAR(255)
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, slug, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_wiki_pages_kb_slug (knowledge_base_id, live_slug),
    KEY idx_wiki_pages_kb_id (knowledge_base_id),
    KEY idx_wiki_pages_page_type (knowledge_base_id, page_type),
    KEY idx_wiki_pages_parent_slug (knowledge_base_id, parent_slug),
    KEY idx_wiki_pages_tree
        (knowledge_base_id, page_type, wiki_path(191), sort_order, title(191)),
    KEY idx_wiki_pages_folder (knowledge_base_id, folder_id),
    KEY idx_wiki_pages_tenant_id (tenant_id),
    KEY idx_wiki_pages_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS wiki_folders (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_name VARCHAR(255)
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_wiki_folders_parent_name
        (knowledge_base_id, parent_id, live_name),
    KEY idx_wiki_folders_parent (knowledge_base_id, parent_id),
    KEY idx_wiki_folders_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    suspected_knowledge_ids JSON NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    reported_by VARCHAR(100) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_wiki_page_issues_tenant_id (tenant_id),
    KEY idx_wiki_page_issues_knowledge_base_id (knowledge_base_id),
    KEY idx_wiki_page_issues_slug (slug),
    KEY idx_wiki_page_issues_status (status)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action VARCHAR(32) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    doc_title TEXT NOT NULL,
    summary TEXT NOT NULL,
    pages_affected JSON NOT NULL DEFAULT (JSON_ARRAY()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_wiki_log_entries_kb_id_desc (knowledge_base_id, id DESC),
    KEY idx_wiki_log_entries_tenant_id (tenant_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS task_pending_ops (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload JSON NOT NULL DEFAULT (JSON_OBJECT()),
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    claimed_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_task_pending_ops_scope (task_type, scope, scope_id, id),
    KEY idx_task_pending_ops_tenant (tenant_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS task_dead_letters (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    payload JSON NOT NULL,
    last_error TEXT NOT NULL,
    fail_count INT NOT NULL,
    failed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_task_dead_letters_scope (scope, scope_id, failed_at DESC),
    KEY idx_task_dead_letters_tenant (tenant_id, failed_at DESC),
    KEY idx_task_dead_letters_task_type (task_type, failed_at DESC)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    knowledge_id VARCHAR(64) NOT NULL,
    attempt INT NOT NULL DEFAULT 1,
    span_id VARCHAR(64) NOT NULL,
    parent_span_id VARCHAR(64) NULL,
    name VARCHAR(255) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    input JSON NULL,
    output JSON NULL,
    metadata JSON NULL,
    error_code VARCHAR(64) NULL,
    error_message TEXT,
    error_detail TEXT,
    started_at DATETIME(6) NULL,
    finished_at DATETIME(6) NULL,
    duration_ms BIGINT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_kpspan_attempt_span (knowledge_id, attempt, span_id),
    KEY idx_kpspan_knowledge_attempt (knowledge_id, attempt),
    KEY idx_kpspan_status_started (status, started_at),
    KEY idx_kpspan_parent (parent_span_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    client_id VARCHAR(512) NOT NULL,
    client_secret TEXT,
    redirect_uri VARCHAR(1024) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY idx_mcp_oauth_clients_tenant_svc (tenant_id, service_id),
    KEY idx_mcp_oauth_clients_service_id (service_id),
    CONSTRAINT fk_mcp_oauth_clients_service
        FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    user_id VARCHAR(512) NOT NULL,
    principal_type VARCHAR(32) NOT NULL,
    principal_id VARCHAR(512) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32) NULL,
    expires_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY idx_mcp_oauth_tokens_tenant_principal_svc
        (tenant_id, principal_type, principal_id, service_id),
    KEY idx_mcp_oauth_tokens_service_id (service_id),
    KEY idx_mcp_oauth_tokens_user_id (user_id(191)),
    KEY idx_mcp_oauth_tokens_principal (principal_type, principal_id(191)),
    CONSTRAINT fk_mcp_oauth_tokens_service
        FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (knowledge_id, tag_id),
    KEY idx_ktr_knowledge (knowledge_id),
    KEY idx_ktr_tag (tag_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS system_settings (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `key` VARCHAR(128) NOT NULL,
    value JSON NOT NULL,
    value_type VARCHAR(16) NOT NULL,
    category VARCHAR(32) NOT NULL,
    description TEXT NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    requires_restart BOOLEAN NOT NULL DEFAULT FALSE,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_system_settings_key (`key`),
    KEY idx_system_settings_category (category)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_api_keys (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(128) NOT NULL,
    key_hash VARCHAR(64) NOT NULL,
    api_key TEXT NOT NULL,
    full_access BOOLEAN NOT NULL DEFAULT FALSE,
    knowledge_base_ids JSON NOT NULL DEFAULT (JSON_ARRAY()),
    capabilities JSON NOT NULL DEFAULT (JSON_ARRAY()),
    last_used_at DATETIME(6) NULL,
    expires_at DATETIME(6) NULL,
    revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_tenant_api_keys_hash (key_hash),
    KEY idx_tenant_api_keys_tenant (tenant_id),
    KEY idx_tenant_api_keys_revoked_at (revoked_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS message_suggestion_sets (
    id VARCHAR(36) NOT NULL,
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
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until DATETIME(6) NULL,
    generated_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY idx_message_suggestion_sets_cache_key
        (tenant_id, assistant_message_id, placement, config_hash, locale),
    KEY idx_message_suggestion_sets_session (tenant_id, session_id, created_at),
    KEY idx_message_suggestion_sets_status (status, lease_until)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS message_suggestion_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id BIGINT UNSIGNED NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_message_suggestion_events_set (suggestion_set_id, created_at),
    KEY idx_message_suggestion_events_session (tenant_id, session_id, created_at),
    KEY idx_message_suggestion_events_type (event_type, created_at),
    CONSTRAINT fk_message_suggestion_events_set
        FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS storage_backends (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_name VARCHAR(255)
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
    live_legacy_provider VARCHAR(32)
        GENERATED ALWAYS AS (
            IF(deleted_at IS NULL AND legacy_alias = TRUE, provider, NULL)
        ) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY idx_storage_backends_name_tenant (tenant_id, live_name),
    UNIQUE KEY idx_storage_backends_legacy_alias
        (tenant_id, live_legacy_provider),
    KEY idx_storage_backends_tenant (tenant_id),
    KEY idx_storage_backends_provider (provider),
    KEY idx_storage_backends_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS resources (
    id VARCHAR(36) NOT NULL,
    handle VARCHAR(22) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    storage_backend_id VARCHAR(36) NULL,
    provider VARCHAR(32) NOT NULL,
    physical_path TEXT NOT NULL,
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
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    live_location_hash VARCHAR(64)
        GENERATED ALWAYS AS (IF(deleted_at IS NULL, location_hash, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uq_resources_handle (handle),
    UNIQUE KEY idx_resources_tenant_location (tenant_id, live_location_hash),
    KEY idx_resources_tenant (tenant_id),
    KEY idx_resources_backend (storage_backend_id),
    KEY idx_resources_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS resource_bindings (
    id VARCHAR(36) NOT NULL,
    resource_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY idx_resource_bindings_unique
        (resource_id, owner_type, owner_id, relation),
    KEY idx_resource_bindings_owner (tenant_id, owner_type, owner_id),
    CONSTRAINT fk_resource_bindings_resource
        FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS resource_access_grants (
    id VARCHAR(36) NOT NULL,
    token_hash VARCHAR(64) NOT NULL,
    resource_id VARCHAR(36) NOT NULL,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_resource_access_grants_token_hash (token_hash),
    KEY idx_resource_access_grants_resource (resource_id),
    KEY idx_resource_access_grants_expires (expires_at),
    CONSTRAINT fk_resource_access_grants_resource
        FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS temporary_documents (
    id VARCHAR(36) NOT NULL,
    tenant_id BIGINT UNSIGNED NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref TEXT NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content TEXT NOT NULL,
    chunks JSON NOT NULL DEFAULT (JSON_ARRAY()),
    image_refs JSON NOT NULL DEFAULT (JSON_ARRAY()),
    metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    processing_options JSON NOT NULL DEFAULT (JSON_OBJECT()),
    token_count INT NOT NULL DEFAULT 0,
    chunk_count INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    ready_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_temporary_documents_scope (tenant_id, session_id),
    KEY idx_temporary_documents_status (status),
    KEY idx_temporary_documents_expires (expires_at),
    KEY idx_temporary_documents_deleted_at (deleted_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

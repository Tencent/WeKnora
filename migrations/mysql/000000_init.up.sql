-- MySQL schema for WeKnora when DB_DRIVER=mysql.
-- Consolidated baseline matching PostgreSQL migration head 000133.
-- MySQL is supported as the business metadata database; vector retrieval stays external.
SET NAMES utf8mb4 COLLATE utf8mb4_0900_bin;
SET time_zone = '+00:00';

CREATE TABLE IF NOT EXISTS tenants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    retriever_engines JSON NOT NULL DEFAULT (JSON_ARRAY()),
    status VARCHAR(50) DEFAULT ('active'),
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    agent_config JSON DEFAULT NULL,
    context_config JSON,
    conversation_config JSON,
    web_search_config JSON DEFAULT NULL,
    parser_engine_config JSON DEFAULT NULL,
    storage_engine_config JSON DEFAULT NULL,
    default_storage_backend_id VARCHAR(36),
    credentials JSON DEFAULT NULL,
    chat_history_config JSON,
    retrieval_config JSON,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT (''),
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description LONGTEXT,
    parameters JSON NOT NULL,
    is_default TINYINT(1) NOT NULL DEFAULT 0,
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    managed_by VARCHAR(32) NOT NULL DEFAULT (''),
    status VARCHAR(50) NOT NULL DEFAULT ('active'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_models_type ON models(type);

CREATE INDEX idx_models_source ON models(source);

CREATE INDEX idx_models_is_builtin ON models(is_builtin);

CREATE INDEX idx_models_managed_by ON models(managed_by);

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    tenant_id BIGINT NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT ('document'),
    chunking_config JSON NOT NULL DEFAULT ('{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\\n\\n", "\\n", "。"], "keep_separator": true}'),
    image_processing_config JSON NOT NULL DEFAULT ('{"enable_multimodal": false, "model_id": ""}'),
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    storage_provider_config JSON DEFAULT NULL,
    vlm_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    extract_config JSON NULL DEFAULT NULL,
    faq_config JSON,
    question_generation_config JSON NULL,
    is_temporary TINYINT(1) NOT NULL DEFAULT 0,
    is_pinned BIGINT NOT NULL DEFAULT 0,
    pinned_at DATETIME(6) NULL,
    asr_config JSON,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    creator_id VARCHAR(36),
    wiki_config JSON,
    indexing_strategy JSON DEFAULT ('{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);

CREATE INDEX idx_knowledge_bases_tenant_vector_store
    ON knowledge_bases(tenant_id, vector_store_id);

CREATE INDEX idx_knowledge_bases_storage_backend
    ON knowledge_bases(tenant_id, storage_backend_id);

CREATE INDEX idx_knowledge_bases_tenant_creator
    ON knowledge_bases(tenant_id, creator_id);

CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description LONGTEXT,
    source VARCHAR(2048) NOT NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT ('unprocessed'),
    enable_status VARCHAR(50) NOT NULL DEFAULT ('enabled'),
    embedding_model_id VARCHAR(64),
    file_name VARCHAR(255),
    file_type VARCHAR(50),
    file_size BIGINT,
    file_path LONGTEXT,
    file_hash VARCHAR(64),
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata JSON,
    custom_metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    tag_id VARCHAR(36),
    summary_status VARCHAR(32) DEFAULT ('none'),
    last_faq_import_result JSON DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT ('web'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    processed_at DATETIME(6),
    error_message LONGTEXT,
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

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
    max_rounds BIGINT NOT NULL DEFAULT 5,
    enable_rewrite TINYINT(1) NOT NULL DEFAULT 1,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT ('fixed'),
    fallback_response LONGTEXT NOT NULL DEFAULT ('很抱歉，我暂时无法回答这个问题。'),
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k BIGINT NOT NULL DEFAULT 10,
    rerank_top_k BIGINT NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters JSON NOT NULL DEFAULT (JSON_OBJECT()),
    agent_config JSON DEFAULT NULL,
    context_config JSON DEFAULT NULL,
    agent_id VARCHAR(36),
    user_id VARCHAR(512),
    is_pinned TINYINT(1) NOT NULL DEFAULT 0,
    pinned_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);

CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);

CREATE INDEX idx_sessions_tenant_user_pin
    ON sessions (tenant_id, user_id, is_pinned, pinned_at, updated_at, deleted_at);

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content LONGTEXT NOT NULL,
    rendered_content LONGTEXT NOT NULL DEFAULT (''),
    knowledge_references JSON NOT NULL DEFAULT (JSON_ARRAY()),
    agent_steps JSON DEFAULT NULL,
    mentioned_items JSON DEFAULT (JSON_ARRAY()),
    images JSON DEFAULT (JSON_ARRAY()),
    is_completed TINYINT(1) NOT NULL DEFAULT 0,
    is_fallback TINYINT(1) NOT NULL DEFAULT 0,
    channel VARCHAR(50) NOT NULL DEFAULT (''),
    agent_id VARCHAR(36) NOT NULL DEFAULT (''),
    agent_tenant_id BIGINT NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT (''),
    execution_context JSON NOT NULL DEFAULT (JSON_OBJECT()),
    agent_duration_ms BIGINT DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_messages_session_id ON messages(session_id);

CREATE INDEX idx_messages_knowledge_id ON messages(knowledge_id);

CREATE INDEX idx_messages_agent_id ON messages(agent_id);

CREATE TABLE IF NOT EXISTS message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT (''),
    agent_tenant_id BIGINT NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT (''),
    status VARCHAR(16) NOT NULL,
    allow_regenerate TINYINT(1) NOT NULL DEFAULT 0,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT (''),
    questions JSON NOT NULL DEFAULT (JSON_ARRAY()),
    model_id VARCHAR(64) NOT NULL DEFAULT (''),
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT (''),
    lease_until DATETIME(6),
    generated_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_message_suggestion_sets_cache_key
    ON message_suggestion_sets(tenant_id, assistant_message_id, placement, config_hash, locale);

CREATE INDEX idx_message_suggestion_sets_session
    ON message_suggestion_sets(tenant_id, session_id, created_at);

CREATE INDEX idx_message_suggestion_sets_status
    ON message_suggestion_sets(status, lease_until);

CREATE TABLE IF NOT EXISTS message_suggestion_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT (''),
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT (''),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_message_suggestion_events_set
    ON message_suggestion_events(suggestion_set_id, created_at);

CREATE INDEX idx_message_suggestion_events_session
    ON message_suggestion_events(tenant_id, session_id, created_at);

CREATE INDEX idx_message_suggestion_events_type
    ON message_suggestion_events(event_type, created_at);

CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content LONGTEXT NOT NULL,
    source_content LONGTEXT NOT NULL DEFAULT (''),
    content_revision BIGINT NOT NULL DEFAULT 0,
    index_status VARCHAR(16) NOT NULL DEFAULT ('ready'),
    last_editor_id VARCHAR(64) NOT NULL DEFAULT (''),
    context_header LONGTEXT NOT NULL DEFAULT (''),
    chunk_index BIGINT NOT NULL,
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    start_at BIGINT NOT NULL,
    end_at BIGINT NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT ('text'),
    parent_chunk_id VARCHAR(36),
    image_info LONGTEXT,
    video_info LONGTEXT,
    relation_chunks JSON,
    indirect_relation_chunks JSON,
    metadata JSON,
    tag_id VARCHAR(36),
    status BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64),
    flags BIGINT NOT NULL DEFAULT 1,
    seq_id BIGINT NOT NULL AUTO_INCREMENT,
    UNIQUE KEY idx_chunks_seq_id (seq_id),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);

CREATE INDEX idx_chunks_parent_id ON chunks(parent_chunk_id);

CREATE INDEX idx_chunks_chunk_type ON chunks(chunk_type);

CREATE INDEX idx_chunks_tag ON chunks(tag_id);

CREATE INDEX idx_chunks_content_hash ON chunks(content_hash);

CREATE INDEX idx_chunks_kb_tenant ON chunks(knowledge_base_id, tenant_id);

CREATE INDEX idx_chunks_knowledge_enabled ON chunks(knowledge_id, is_enabled, deleted_at);

CREATE TABLE IF NOT EXISTS chunk_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    revision BIGINT NOT NULL,
    content LONGTEXT NOT NULL DEFAULT (''),
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    editor_id VARCHAR(64) NOT NULL DEFAULT (''),
    edit_source VARCHAR(16) NOT NULL DEFAULT ('user'),
    edited_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE(chunk_id, revision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_chunk_revisions_tenant_chunk ON chunk_revisions(tenant_id, chunk_id);

    -- Per-user JSON preferences (memory toggle, future UI knobs).
    -- SQLite has no JSONB; store as TEXT and let GORM (de)serialise via
    -- the driver.Valuer / sql.Scanner methods on types.UserPreferences.
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id BIGINT,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    can_access_all_tenants TINYINT(1) NOT NULL DEFAULT 0,
    preferences JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_users_username ON users(username);

CREATE INDEX idx_users_email ON users(email);

CREATE INDEX idx_users_tenant_id ON users(tenant_id);

CREATE INDEX idx_users_deleted_at ON users(deleted_at);

CREATE TABLE IF NOT EXISTS auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token LONGTEXT NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    is_revoked TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);

CREATE INDEX idx_auth_tokens_token ON auth_tokens(token(191));

CREATE INDEX idx_auth_tokens_token_type ON auth_tokens(token_type);

CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

-- Tenant RBAC membership is unique only while the row is live (PG 000043).
CREATE TABLE IF NOT EXISTS tenant_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT ('contributor'),
    status VARCHAR(20) NOT NULL DEFAULT ('active'),
    invited_by VARCHAR(36),
    joined_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id, active_row);

CREATE INDEX idx_tenant_members_tenant_role
    ON tenant_members(tenant_id, role);

CREATE INDEX idx_tenant_members_user
    ON tenant_members(user_id);

-- audit_logs is the generic per-tenant durability for RBAC events
-- (and future KB / agent / datasource events). Sqlite mirror of the
-- 000044_audit_log migration; same column shape with INTEGER for the
-- BIGSERIAL id and TEXT in place of JSONB for details.
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    actor_user_id VARCHAR(36) NOT NULL DEFAULT (''),
    actor_role VARCHAR(32) NOT NULL DEFAULT (''),
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL DEFAULT (''),
    target_id VARCHAR(64) NOT NULL DEFAULT (''),
    target_user_id VARCHAR(36) NOT NULL DEFAULT (''),
    request_path VARCHAR(512) NOT NULL DEFAULT (''),
    request_method VARCHAR(16) NOT NULL DEFAULT (''),
    outcome VARCHAR(16) NOT NULL DEFAULT ('success'),
    details JSON NOT NULL DEFAULT (JSON_OBJECT()),
    scope_type VARCHAR(32) NOT NULL DEFAULT (''),
    scope_id VARCHAR(64) NOT NULL DEFAULT (''),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_audit_logs_tenant_id_desc
    ON audit_logs(tenant_id, id DESC);

CREATE INDEX idx_audit_logs_actor
    ON audit_logs(actor_user_id);

CREATE INDEX idx_audit_logs_tenant_action
    ON audit_logs(tenant_id, action);

CREATE INDEX idx_audit_logs_created_at
    ON audit_logs(created_at);

CREATE INDEX idx_audit_logs_tenant_scope_desc
    ON audit_logs(tenant_id, scope_type, scope_id, id DESC);

-- user_resource_favorites — sqlite mirror of migration 000047. Same
-- composite PK (user_id, tenant_id, resource_type, resource_id) so the
-- GORM model and FirstOrCreate idempotency carry over.
CREATE TABLE IF NOT EXISTS user_resource_favorites (
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_user_resource_favorites_user_tenant_type_created_at
    ON user_resource_favorites(user_id, tenant_id, resource_type, created_at DESC);

CREATE INDEX idx_user_resource_favorites_tenant_id
    ON user_resource_favorites(tenant_id);

-- user_kb_pins — sqlite mirror of migration 000050. Per-(user, tenant)
-- pinned knowledge bases; replaces the tenant-wide knowledge_bases.is_pinned
-- column for ordering purposes. The legacy column on knowledge_bases is
-- still defined above for back-compat with existing rows but is no longer
-- written by the application.
CREATE TABLE IF NOT EXISTS user_kb_pins (
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, user_id, kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_user_kb_pins_user_tenant_pinned_at
    ON user_kb_pins(tenant_id, user_id, pinned_at DESC);

-- tenant_invitations — sqlite mirror of migration 000048. SQLite supports
-- partial unique indexes too, so the same "one pending per (tenant,
-- invitee)" guard can be applied verbatim.
CREATE TABLE IF NOT EXISTS tenant_invitations (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL,
    invited_by VARCHAR(36),
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT ('pending'),
    message VARCHAR(500),
    expires_at DATETIME(6) NOT NULL,
    responded_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    pending_active TINYINT(1) GENERATED ALWAYS AS (CASE WHEN status = 'pending' AND deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id, pending_active);

CREATE INDEX idx_tenant_invitations_tenant
    ON tenant_invitations(tenant_id);

CREATE INDEX idx_tenant_invitations_invitee
    ON tenant_invitations(invitee_user_id);

CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order BIGINT NOT NULL DEFAULT 0,
    seq_id BIGINT NOT NULL AUTO_INCREMENT,
    UNIQUE KEY idx_knowledge_tags_seq_id (seq_id),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_knowledge_tags_kb_name ON knowledge_tags(tenant_id, knowledge_base_id, name);

CREATE INDEX idx_knowledge_tags_kb ON knowledge_tags(tenant_id, knowledge_base_id);

CREATE TABLE IF NOT EXISTS mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    enabled TINYINT(1) DEFAULT 1,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512),
    headers LONGTEXT,
    auth_config LONGTEXT,
    advanced_config LONGTEXT,
    stdio_config LONGTEXT,
    env_vars LONGTEXT,
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_mcp_services_tenant_id ON mcp_services(tenant_id);

CREATE INDEX idx_mcp_services_enabled ON mcp_services(enabled);

CREATE INDEX idx_mcp_services_is_builtin ON mcp_services(is_builtin);

CREATE INDEX idx_mcp_services_deleted_at ON mcp_services(deleted_at);

CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_mcp_tool_approvals_tenant_svc_tool ON mcp_tool_approvals(tenant_id, service_id, tool_name);

CREATE INDEX idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    client_id VARCHAR(512) NOT NULL,
    client_secret LONGTEXT,
    redirect_uri VARCHAR(1024),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_mcp_oauth_clients_tenant_svc ON mcp_oauth_clients(tenant_id, service_id);

CREATE INDEX idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    access_token LONGTEXT,
    refresh_token LONGTEXT,
    token_type VARCHAR(32),
    expires_at DATETIME(6),
    refresh_lease_id VARCHAR(36),
    refresh_lease_until DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens(tenant_id, user_id, service_id);

CREATE INDEX idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);

CREATE INDEX idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);

CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    avatar VARCHAR(64),
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    tenant_id BIGINT NOT NULL,
    created_by VARCHAR(36),
    runnable_by_viewer TINYINT(1) NOT NULL DEFAULT 1,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    PRIMARY KEY (id, tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_custom_agents_tenant_id ON custom_agents(tenant_id);

CREATE INDEX idx_custom_agents_is_builtin ON custom_agents(is_builtin);

CREATE INDEX idx_custom_agents_deleted_at ON custom_agents(deleted_at);

    -- Plan 3 (#1303): owning tenant pinned at create time; see migration 000046.
CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id BIGINT NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval TINYINT(1) DEFAULT 0,
    invite_code_expires_at DATETIME(6),
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT (''),
    searchable TINYINT(1) NOT NULL DEFAULT 0,
    member_limit BIGINT NOT NULL DEFAULT 50,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_organizations_owner_id ON organizations(owner_id);

CREATE INDEX idx_organizations_owner_tenant ON organizations(owner_tenant_id);

CREATE INDEX idx_organizations_deleted_at ON organizations(deleted_at);

CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT ('viewer'),
    representative_user_id VARCHAR(36) NOT NULL DEFAULT (''),
    joined_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_org_tenant_members_unique ON organization_tenant_members(organization_id, tenant_id);

CREATE INDEX idx_org_tenant_members_by_tenant ON organization_tenant_members(tenant_id);

CREATE INDEX idx_org_tenant_members_role ON organization_tenant_members(organization_id, role);

CREATE TABLE IF NOT EXISTS kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT ('viewer'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);

CREATE INDEX idx_kb_shares_org_id ON kb_shares(organization_id);

CREATE INDEX idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);

CREATE INDEX idx_kb_shares_deleted_at ON kb_shares(deleted_at);

CREATE TABLE IF NOT EXISTS organization_join_requests (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT ('pending'),
    requested_role VARCHAR(32) NOT NULL DEFAULT ('viewer'),
    request_type VARCHAR(32) NOT NULL DEFAULT ('join'),
    prev_role VARCHAR(32),
    message LONGTEXT,
    reviewed_by VARCHAR(36),
    reviewed_at DATETIME(6),
    review_message LONGTEXT,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    pending_active TINYINT(1) GENERATED ALWAYS AS (CASE WHEN status = 'pending' THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_org_join_requests_org_id ON organization_join_requests(organization_id);

CREATE INDEX idx_org_join_requests_user_id ON organization_join_requests(user_id);

CREATE INDEX idx_org_join_requests_status ON organization_join_requests(status);

-- Plan 3 (#1303): at most one pending request per (org, tenant, type).
-- Approved/rejected rows are not constrained so the audit trail stays.
CREATE UNIQUE INDEX uq_org_join_requests_pending_per_tenant
    ON organization_join_requests(organization_id, tenant_id, request_type, pending_active);

CREATE TABLE IF NOT EXISTS agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT ('viewer'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_agent_shares_agent_id ON agent_shares(agent_id);

CREATE INDEX idx_agent_shares_org_id ON agent_shares(organization_id);

CREATE INDEX idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);

CREATE INDEX idx_agent_shares_deleted_at ON agent_shares(deleted_at);

CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_tenant_disabled_shared_agents_tenant_id ON tenant_disabled_shared_agents(tenant_id);

CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id VARCHAR(36) PRIMARY KEY,
    platform VARCHAR(20) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    chat_id VARCHAR(128) NOT NULL DEFAULT (''),
    session_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) DEFAULT (''),
    im_channel_id VARCHAR(36) DEFAULT (''),
    thread_id VARCHAR(128) NOT NULL DEFAULT (''),
    status VARCHAR(20) NOT NULL DEFAULT ('active'),
    metadata JSON DEFAULT (JSON_OBJECT()),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_lookup TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED,
    active_thread_lookup TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND thread_id <> '' THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id, active_lookup);

CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id, agent_id, active_thread_lookup);

CREATE INDEX idx_im_channel_tenant ON im_channel_sessions (tenant_id);

CREATE INDEX idx_im_channel_session ON im_channel_sessions (session_id);

CREATE INDEX idx_im_channel_sessions_channel
    ON im_channel_sessions (im_channel_id);

CREATE TABLE IF NOT EXISTS im_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT (''),
    enabled BIGINT NOT NULL DEFAULT 1,
    mode VARCHAR(20) NOT NULL DEFAULT ('websocket'),
    output_mode VARCHAR(20) NOT NULL DEFAULT ('stream'),
    credentials JSON NOT NULL DEFAULT (JSON_OBJECT()),
    knowledge_base_id VARCHAR(36) DEFAULT (''),
    bot_identity VARCHAR(255) NOT NULL DEFAULT (''),
    session_mode VARCHAR(20) NOT NULL DEFAULT ('user'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_bot_identity TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND bot_identity <> '' THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_im_channels_tenant ON im_channels (tenant_id);

CREATE INDEX idx_im_channels_agent ON im_channels (agent_id);

CREATE UNIQUE INDEX idx_im_channels_bot_identity
    ON im_channels (bot_identity, active_bot_identity);

CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT ('builtin-quick-answer'),
    name VARCHAR(255) NOT NULL DEFAULT (''),
    enabled BIGINT NOT NULL DEFAULT 1,
    publish_token VARCHAR(64) NOT NULL DEFAULT (''),
    allowed_origins JSON NOT NULL DEFAULT (JSON_ARRAY()),
    welcome_message LONGTEXT NOT NULL DEFAULT (''),
    rate_limit_per_minute BIGINT NOT NULL DEFAULT 30,
    rate_limit_per_day BIGINT NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT (''),
    page_title VARCHAR(255) NOT NULL DEFAULT (''),
    header_title_mode VARCHAR(32) NOT NULL DEFAULT ('channel'),
    show_suggested_questions BIGINT NOT NULL DEFAULT 1,
    widget_position VARCHAR(32) NOT NULL DEFAULT ('bottom-right'),
    allow_web_search BIGINT NOT NULL DEFAULT 0,
    allow_file_upload BIGINT NOT NULL DEFAULT 0,
    default_locale VARCHAR(16) NOT NULL DEFAULT (''),
    webhook_url VARCHAR(512) NOT NULL DEFAULT (''),
    webhook_secret VARCHAR(128) NOT NULL DEFAULT (''),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_publish_token TINYINT(1) GENERATED ALWAYS AS (CASE WHEN publish_token <> '' AND deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_embed_channels_tenant ON embed_channels (tenant_id);

CREATE INDEX idx_embed_channels_agent ON embed_channels (agent_id);

CREATE UNIQUE INDEX idx_embed_channels_publish_token
    ON embed_channels (publish_token, active_publish_token);

CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config JSON,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT ('incremental'),
    status VARCHAR(32) DEFAULT ('active'),
    conflict_strategy VARCHAR(32) DEFAULT ('overwrite'),
    sync_deletions BIGINT DEFAULT 1,
    last_sync_at DATETIME(6) NULL,
    last_sync_cursor LONGTEXT,
    last_sync_result JSON,
    error_message LONGTEXT,
    sync_log_retention_days BIGINT DEFAULT 30,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_data_sources_tenant_id ON data_sources (tenant_id);

CREATE INDEX idx_data_sources_knowledge_base_id ON data_sources (knowledge_base_id);

CREATE INDEX idx_data_sources_type ON data_sources (type);

CREATE INDEX idx_data_sources_status ON data_sources (status);

CREATE INDEX idx_data_sources_deleted_at ON data_sources (deleted_at);

CREATE TABLE IF NOT EXISTS sync_logs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6) NULL,
    items_total BIGINT DEFAULT 0,
    items_created BIGINT DEFAULT 0,
    items_updated BIGINT DEFAULT 0,
    items_deleted BIGINT DEFAULT 0,
    items_skipped BIGINT DEFAULT 0,
    items_failed BIGINT DEFAULT 0,
    error_message LONGTEXT,
    result JSON,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_sync_logs_data_source_id ON sync_logs (data_source_id);

CREATE INDEX idx_sync_logs_tenant_id ON sync_logs (tenant_id);

CREATE INDEX idx_sync_logs_status ON sync_logs (status);

CREATE INDEX idx_sync_logs_started_at ON sync_logs (started_at);

CREATE TABLE IF NOT EXISTS web_search_providers (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description LONGTEXT,
    parameters JSON,
    is_default BIGINT DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_web_search_providers_tenant_id ON web_search_providers (tenant_id);

CREATE INDEX idx_web_search_providers_provider ON web_search_providers (provider);

CREATE INDEX idx_web_search_providers_deleted_at ON web_search_providers (deleted_at);

CREATE TABLE IF NOT EXISTS vector_stores (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    index_config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    tenant_id BIGINT NOT NULL,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_vector_stores_name_tenant
    ON vector_stores(name, tenant_id, active_row);

CREATE INDEX idx_vector_stores_tenant_id ON vector_stores(tenant_id);

CREATE INDEX idx_vector_stores_engine_type ON vector_stores(engine_type);

CREATE INDEX idx_vector_stores_deleted_at ON vector_stores(deleted_at);

CREATE TABLE IF NOT EXISTS storage_backends (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    source VARCHAR(16) NOT NULL DEFAULT ('user'),
    status VARCHAR(16) NOT NULL DEFAULT ('active'),
    legacy_alias TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED,
    legacy_alias_active TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND legacy_alias = 1 THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_storage_backends_name_tenant
    ON storage_backends(tenant_id, name, active_row);

CREATE UNIQUE INDEX idx_storage_backends_legacy_alias
    ON storage_backends(tenant_id, provider, legacy_alias_active);

CREATE INDEX idx_storage_backends_tenant ON storage_backends(tenant_id);

CREATE TABLE IF NOT EXISTS resources (
    id VARCHAR(36) PRIMARY KEY,
    handle VARCHAR(22) NOT NULL UNIQUE,
    tenant_id BIGINT NOT NULL,
    storage_backend_id VARCHAR(36),
    provider VARCHAR(32) NOT NULL,
    physical_path LONGTEXT NOT NULL,
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT ('file'),
    mime_type VARCHAR(255) NOT NULL DEFAULT (''),
    original_name VARCHAR(1024) NOT NULL DEFAULT (''),
    size BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT (''),
    lifecycle VARCHAR(16) NOT NULL DEFAULT ('persistent'),
    expires_at DATETIME(6),
    state VARCHAR(16) NOT NULL DEFAULT ('active'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_resources_tenant_location
    ON resources(tenant_id, location_hash, active_row);

CREATE INDEX idx_resources_tenant ON resources(tenant_id);

CREATE INDEX idx_resources_backend ON resources(storage_backend_id);

CREATE TABLE IF NOT EXISTS resource_bindings (
    id VARCHAR(36) PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT ('attachment'),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_resource_bindings_unique
    ON resource_bindings(resource_id, owner_type, owner_id, relation);

CREATE INDEX idx_resource_bindings_owner
    ON resource_bindings(tenant_id, owner_type, owner_id);

CREATE TABLE IF NOT EXISTS resource_access_grants (
    id VARCHAR(36) PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    resource_id VARCHAR(36) NOT NULL,
    access_scope VARCHAR(16) NOT NULL DEFAULT ('read'),
    expires_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_resource_access_grants_resource
    ON resource_access_grants(resource_id);

CREATE INDEX idx_resource_access_grants_expires
    ON resource_access_grants(expires_at);

CREATE TABLE IF NOT EXISTS tenant_api_keys (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT,
    scope_type VARCHAR(32) NOT NULL DEFAULT ('tenant')
        CHECK (scope_type IN ('tenant', 'platform')),
    name VARCHAR(255) NOT NULL,
    key_hash VARCHAR(128) NOT NULL UNIQUE,
    api_key LONGTEXT NOT NULL DEFAULT (''),
    full_access TINYINT(1) NOT NULL DEFAULT 0,
    knowledge_base_ids JSON NOT NULL DEFAULT (JSON_ARRAY()),
    capabilities JSON NOT NULL DEFAULT (JSON_ARRAY()),
    last_used_at DATETIME(6),
    expires_at DATETIME(6),
    revoked_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = 0)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);

CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);

CREATE INDEX idx_tenant_api_keys_scope_type ON tenant_api_keys(scope_type);

CREATE TABLE IF NOT EXISTS temporary_documents (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref JSON NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT (''),
    file_size BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT ('uploaded'),
    content LONGTEXT NOT NULL DEFAULT (''),
    chunks JSON NOT NULL DEFAULT (JSON_ARRAY()),
    image_refs JSON NOT NULL DEFAULT (JSON_ARRAY()),
    metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    processing_options JSON NOT NULL DEFAULT (JSON_OBJECT()),
    token_count BIGINT NOT NULL DEFAULT 0,
    chunk_count BIGINT NOT NULL DEFAULT 0,
    error_message LONGTEXT NOT NULL DEFAULT (''),
    expires_at DATETIME(6) NOT NULL,
    started_at DATETIME(6),
    ready_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_temporary_documents_scope ON temporary_documents(tenant_id, session_id);

CREATE INDEX idx_temporary_documents_status ON temporary_documents(status);

CREATE INDEX idx_temporary_documents_expires ON temporary_documents(expires_at);

-- ---------------------------------------------------------------------------
-- Wiki (consolidated from Postgres migrations 000037, 000040, 000061, 000075)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_pages (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug              VARCHAR(255) NOT NULL,
    title             VARCHAR(512) NOT NULL DEFAULT (''),
    page_type         VARCHAR(32) NOT NULL DEFAULT ('summary'),
    status            VARCHAR(32) NOT NULL DEFAULT ('published'),
    content           LONGTEXT NOT NULL DEFAULT (''),
    summary           LONGTEXT NOT NULL DEFAULT (''),
    parent_slug       VARCHAR(255) NOT NULL DEFAULT (''),
    folder_id         VARCHAR(36) NOT NULL DEFAULT (''),
    category_path     JSON DEFAULT (JSON_ARRAY()),
    wiki_path         VARCHAR(1024) NOT NULL DEFAULT (''),
    depth             BIGINT NOT NULL DEFAULT 0,
    sort_order        BIGINT NOT NULL DEFAULT 0,
    source_refs       JSON DEFAULT (JSON_ARRAY()),
    chunk_refs        JSON DEFAULT (JSON_ARRAY()),
    in_links          JSON DEFAULT (JSON_ARRAY()),
    out_links         JSON DEFAULT (JSON_ARRAY()),
    page_metadata     JSON DEFAULT (JSON_OBJECT()),
    aliases           JSON DEFAULT (JSON_ARRAY()),
    version           BIGINT NOT NULL DEFAULT 1,
    last_edit_source  VARCHAR(16) NOT NULL DEFAULT (''),
    last_editor_id    VARCHAR(64) NOT NULL DEFAULT (''),
    created_at        DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at        DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at        DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_wiki_pages_kb_slug
    ON wiki_pages (knowledge_base_id, slug, active_row);

CREATE INDEX idx_wiki_pages_kb_id
    ON wiki_pages (knowledge_base_id);

CREATE INDEX idx_wiki_pages_page_type
    ON wiki_pages (knowledge_base_id, page_type);

CREATE INDEX idx_wiki_pages_parent_slug
    ON wiki_pages (knowledge_base_id, parent_slug);

CREATE INDEX idx_wiki_pages_tree
    ON wiki_pages (knowledge_base_id, page_type, wiki_path(191), sort_order, title(191));

CREATE INDEX idx_wiki_pages_folder
    ON wiki_pages (knowledge_base_id, folder_id);

CREATE INDEX idx_wiki_pages_tenant_id
    ON wiki_pages (tenant_id);

CREATE INDEX idx_wiki_pages_deleted_at
    ON wiki_pages (deleted_at);

CREATE TABLE IF NOT EXISTS wiki_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT (''),
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT (''),
    depth             BIGINT NOT NULL DEFAULT 0,
    sort_order        BIGINT NOT NULL DEFAULT 0,
    created_at        DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at        DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at        DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_wiki_folders_parent_name
    ON wiki_folders (knowledge_base_id, parent_id, name, active_row);

CREATE INDEX idx_wiki_folders_parent
    ON wiki_folders (knowledge_base_id, parent_id);

CREATE INDEX idx_wiki_folders_deleted_at
    ON wiki_folders (deleted_at);

CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id                    VARCHAR(36) PRIMARY KEY,
    tenant_id             BIGINT NOT NULL,
    knowledge_base_id     VARCHAR(36) NOT NULL,
    slug                  VARCHAR(255) NOT NULL,
    issue_type            VARCHAR(50) NOT NULL,
    description           LONGTEXT NOT NULL,
    suspected_knowledge_ids JSON,
    status                VARCHAR(20) NOT NULL DEFAULT ('pending'),
    reported_by           VARCHAR(100) NOT NULL,
    created_at            DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at            DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at            DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_wiki_page_issues_tenant_id
    ON wiki_page_issues(tenant_id);

CREATE INDEX idx_wiki_page_issues_knowledge_base_id
    ON wiki_page_issues(knowledge_base_id);

CREATE INDEX idx_wiki_page_issues_slug
    ON wiki_page_issues(slug);

CREATE INDEX idx_wiki_page_issues_status
    ON wiki_page_issues(status);

CREATE TABLE IF NOT EXISTS wiki_page_revisions (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id           VARCHAR(36) NOT NULL,
    slug              VARCHAR(255) NOT NULL,
    version           BIGINT NOT NULL,
    title             VARCHAR(512) NOT NULL DEFAULT (''),
    page_type         VARCHAR(32) NOT NULL DEFAULT ('summary'),
    status            VARCHAR(32) NOT NULL DEFAULT ('published'),
    content           LONGTEXT NOT NULL DEFAULT (''),
    summary           LONGTEXT NOT NULL DEFAULT (''),
    aliases           JSON DEFAULT (JSON_ARRAY()),
    edit_source       VARCHAR(16) NOT NULL DEFAULT (''),
    editor_id         VARCHAR(64) NOT NULL DEFAULT (''),
    edited_at         DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    created_at        DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_wiki_page_revisions_page_version
    ON wiki_page_revisions (page_id, version);

CREATE INDEX idx_wiki_page_revisions_kb_slug
    ON wiki_page_revisions (knowledge_base_id, slug);

-- Post-000078 schema consolidated from PostgreSQL 000079..000117 and SQLite mirrors.

-- 000002_knowledge_folder_path.up.sql
ALTER TABLE knowledges ADD COLUMN folder_path VARCHAR(1024) NOT NULL DEFAULT ('');

CREATE INDEX idx_knowledges_folder_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path(191));

-- 000003_knowledge_base_auto_tag_config.up.sql
ALTER TABLE knowledge_bases ADD COLUMN auto_tag_config JSON;

-- 000004_memory.up.sql
CREATE TABLE IF NOT EXISTS memory_subjects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    block_text LONGTEXT NOT NULL DEFAULT (''),
    block_updated_at DATETIME(6),
    item_count BIGINT NOT NULL DEFAULT 0,
    last_extracted_at DATETIME(6),
    extract_cursor DATETIME(6),
    pending_sessions JSON,
    extract_scheduled_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS memory_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    content LONGTEXT NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT (''),
    normalized_key VARCHAR(255) NOT NULL DEFAULT (''),
    importance BIGINT NOT NULL DEFAULT 3,
    origin VARCHAR(16) NOT NULL DEFAULT ('extracted'),
    status VARCHAR(16) NOT NULL DEFAULT ('active'),
    source_session_id VARCHAR(36),
    source_message_id VARCHAR(36),
    valid_from DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    invalid_at DATETIME(6),
    expires_at DATETIME(6),
    superseded_by VARCHAR(36),
    last_used_at DATETIME(6),
    use_count BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS memory_tombstones (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT (''),
    fingerprint VARCHAR(64) NOT NULL,
    source_message_id VARCHAR(36),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS memory_topic_stats (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    normalized_key VARCHAR(255) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT (''),
    hits BIGINT NOT NULL DEFAULT 0,
    last_seen_at DATETIME(6),
    promoted_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS memory_doc_affinity (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT (''),
    title VARCHAR(512) NOT NULL DEFAULT (''),
    hits BIGINT NOT NULL DEFAULT 0,
    last_used_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS memory_item_embeddings (
    item_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT (''),
    dims BIGINT NOT NULL DEFAULT 0,
    vector BLOB,
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

ALTER TABLE tenants ADD COLUMN memory_config JSON;

ALTER TABLE messages ADD COLUMN used_memories JSON;

ALTER TABLE memory_subjects ADD COLUMN consolidated_at DATETIME(6);

ALTER TABLE memory_subjects ADD COLUMN forced_consolidated_at DATETIME(6);

ALTER TABLE memory_topic_stats ADD COLUMN aliases JSON NOT NULL DEFAULT (JSON_ARRAY());

CREATE UNIQUE INDEX idx_memory_subjects_scope
    ON memory_subjects (tenant_id, subject_id);

CREATE INDEX idx_memory_items_scope
    ON memory_items (tenant_id, subject_id, status);

CREATE INDEX idx_memory_items_key
    ON memory_items (tenant_id, subject_id, normalized_key(191));

CREATE INDEX idx_memory_tombstones_scope
    ON memory_tombstones (tenant_id, subject_id);

CREATE UNIQUE INDEX idx_mem_tomb_fp
    ON memory_tombstones (tenant_id, subject_id, fingerprint);

CREATE INDEX idx_mem_emb_scope
    ON memory_item_embeddings (tenant_id, subject_id);

-- 000005_messages_attachments_and_invitation_fields.up.sql
ALTER TABLE messages ADD COLUMN attachments JSON NOT NULL DEFAULT (JSON_ARRAY());

ALTER TABLE tenant_invitations ADD COLUMN token VARCHAR(64) NOT NULL DEFAULT ('');

ALTER TABLE tenant_invitations ADD COLUMN accepted_count BIGINT NOT NULL DEFAULT 0;

-- 000006_task_queue_and_dead_letters.up.sql
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    op          VARCHAR(32) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT (''),
    payload LONGTEXT NOT NULL DEFAULT ('{}'),
    fail_count  BIGINT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    claimed_at  DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS task_dead_letters (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    related_id  VARCHAR(64) NOT NULL DEFAULT (''),
    payload LONGTEXT NOT NULL,
    last_error LONGTEXT NOT NULL DEFAULT (''),
    fail_count  BIGINT NOT NULL,
    failed_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_task_pending_ops_scope
    ON task_pending_ops (task_type, scope, scope_id, id);

CREATE INDEX idx_task_pending_ops_tenant
    ON task_pending_ops (tenant_id);

CREATE INDEX idx_task_dead_letters_scope
    ON task_dead_letters (scope, scope_id, failed_at DESC);

CREATE INDEX idx_task_dead_letters_tenant
    ON task_dead_letters (tenant_id, failed_at DESC);

CREATE INDEX idx_task_dead_letters_task_type
    ON task_dead_letters (task_type, failed_at DESC);

-- 000007_system_admin_and_settings.up.sql
CREATE TABLE IF NOT EXISTS system_settings (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    `key` VARCHAR(128) NOT NULL UNIQUE,
    value LONGTEXT NOT NULL,
    value_type       VARCHAR(16)  NOT NULL,
    category         VARCHAR(32)  NOT NULL,
    description LONGTEXT NOT NULL DEFAULT (''),
    is_secret        TINYINT(1) NOT NULL DEFAULT 0,
    requires_restart TINYINT(1) NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT (''),
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

ALTER TABLE users ADD COLUMN is_system_admin TINYINT(1) NOT NULL DEFAULT 0;

CREATE INDEX idx_users_is_system_admin ON users (is_system_admin);

CREATE INDEX idx_system_settings_category
    ON system_settings (category);

-- 000008_processing_spans_and_pending_subtasks.up.sql
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    knowledge_id    VARCHAR(64) NOT NULL,
    attempt         BIGINT NOT NULL DEFAULT 1,
    span_id         VARCHAR(64) NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(255) NOT NULL,
    kind            VARCHAR(16) NOT NULL,
    status          VARCHAR(16) NOT NULL,
    input JSON,
    output JSON,
    metadata JSON,
    error_code      VARCHAR(64),
    error_message LONGTEXT,
    error_detail LONGTEXT,
    started_at      DATETIME(6),
    finished_at     DATETIME(6),
    duration_ms     BIGINT,
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

ALTER TABLE knowledges ADD COLUMN pending_subtasks_count BIGINT NOT NULL DEFAULT 0;

CREATE INDEX idx_kpspan_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);

CREATE INDEX idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);

CREATE INDEX idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id);

-- 000009_embed_channel_memory_flag.up.sql
ALTER TABLE embed_channels ADD COLUMN allow_memory TINYINT(1) NOT NULL DEFAULT 0;

-- 000010_knowledge_multi_tags.up.sql
CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id       VARCHAR(36) NOT NULL,
    created_at   DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (knowledge_id, tag_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_ktr_knowledge
    ON knowledge_tag_relations (knowledge_id);

CREATE INDEX idx_ktr_tag
    ON knowledge_tag_relations (tag_id);

-- 000011_principal_model.up.sql
ALTER TABLE tenants ADD COLUMN api_principal_config JSON;

ALTER TABLE mcp_oauth_tokens ADD COLUMN principal_type VARCHAR(32);

ALTER TABLE mcp_oauth_tokens ADD COLUMN principal_id VARCHAR(512);

CREATE INDEX idx_mcp_oauth_tokens_principal
    ON mcp_oauth_tokens(principal_type, principal_id);

-- 000012_message_usage.up.sql
ALTER TABLE messages ADD COLUMN `usage` JSON;

-- 000013_mcp_tool_enabled.up.sql
ALTER TABLE mcp_tool_approvals ADD COLUMN enabled TINYINT(1) NOT NULL DEFAULT 1;

-- 000014_browser_authorization.up.sql
CREATE TABLE browser_devices (
 scope_key VARCHAR(32) PRIMARY KEY,
 id VARCHAR(32) NOT NULL UNIQUE,
 tenant BIGINT NOT NULL,
 `user` VARCHAR(36) NOT NULL,
 label VARCHAR(100) NOT NULL,
 token_hash VARCHAR(64) NOT NULL UNIQUE,
 previous_hash VARCHAR(64) NOT NULL DEFAULT (''),
 previous_until DATETIME(6) NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 renew_after DATETIME(6) NOT NULL,
 created_at DATETIME(6) NOT NULL,
 last_seen_at DATETIME(6) NOT NULL,
 revoked_at DATETIME(6),
 owner VARCHAR(32) NOT NULL DEFAULT (''),
 owner_url VARCHAR(500) NOT NULL DEFAULT (''),
 lease_key VARCHAR(32) NOT NULL DEFAULT (''),
 lease_until DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE browser_pairings (
 scope_key VARCHAR(32) PRIMARY KEY,
 token_hash VARCHAR(64) NOT NULL UNIQUE,
 tenant BIGINT NOT NULL,
 `user` VARCHAR(36) NOT NULL,
 expires_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE browser_task_interruptions (
 scope_key VARCHAR(32) NOT NULL,
 session VARCHAR(36) NOT NULL,
 PRIMARY KEY (scope_key, session)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX browser_pairings_expiry ON browser_pairings(expires_at);

-- 000015_memory_consistency.up.sql
CREATE TABLE memory_extraction_sessions (
 tenant_id BIGINT NOT NULL,
 subject_id VARCHAR(512) NOT NULL,
 session_id VARCHAR(36) NOT NULL,
 revision BIGINT NOT NULL DEFAULT 0,
 cursor_at DATETIME(6), cursor_id VARCHAR(36) NOT NULL DEFAULT (''),
 pending TINYINT(1) NOT NULL DEFAULT 0,
 failure_count BIGINT NOT NULL DEFAULT 0,
 failure_code VARCHAR(64) NOT NULL DEFAULT (''),
 failed_from_at DATETIME(6), failed_from_id VARCHAR(36) NOT NULL DEFAULT (''),
 failed_to_at DATETIME(6), failed_to_id VARCHAR(36) NOT NULL DEFAULT (''),
 failed_at DATETIME(6), updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY (tenant_id, subject_id, session_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

ALTER TABLE memory_subjects ADD COLUMN extraction_state JSON;

ALTER TABLE memory_items ADD COLUMN replaces_id VARCHAR(36) NOT NULL DEFAULT ('');

CREATE INDEX idx_memory_extraction_pending ON memory_extraction_sessions (tenant_id, subject_id, pending, updated_at, session_id);

CREATE INDEX idx_memory_replaces ON memory_items (tenant_id, subject_id, replaces_id, status);

-- 000016_memory_vector_search.up.sql
CREATE INDEX idx_mem_emb_search
    ON memory_item_embeddings (tenant_id, subject_id, model_id, dims);

-- 000018_session_fork.up.sql
ALTER TABLE sessions ADD COLUMN parent_session_id VARCHAR(36);

ALTER TABLE sessions ADD COLUMN forked_from_message_id VARCHAR(36);

ALTER TABLE sessions ADD COLUMN fork_bootstrap JSON;

ALTER TABLE messages ADD COLUMN sandbox_checkpoint JSON;

CREATE INDEX idx_sessions_parent_session_id
    ON sessions (parent_session_id);

-- 000019_fork_snapshot_lease.up.sql
CREATE TABLE IF NOT EXISTS fork_snapshot_leases (
    snapshot_id VARCHAR(255) PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    sandbox_config_id VARCHAR(36) NOT NULL DEFAULT (''),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_fork_snapshot_leases_created_at
    ON fork_snapshot_leases (created_at);

-- 000021_knowledge_profiles.up.sql
ALTER TABLE knowledges ADD COLUMN profile JSON;

ALTER TABLE knowledge_bases ADD COLUMN profile_config JSON;

ALTER TABLE knowledge_bases ADD COLUMN generated_profile JSON;

-- 000022_mcp_endpoints.up.sql
CREATE TABLE IF NOT EXISTS mcp_endpoints (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT (''),
    description LONGTEXT NOT NULL DEFAULT (''),
    enabled BIGINT NOT NULL DEFAULT 1,
    token_hash VARCHAR(64) NOT NULL DEFAULT (''),
    token_hint VARCHAR(16) NOT NULL DEFAULT (''),
    knowledge_base_ids JSON NOT NULL DEFAULT (JSON_ARRAY()),
    tools JSON NOT NULL DEFAULT (JSON_ARRAY()),
    default_agent_id VARCHAR(36) NOT NULL DEFAULT (''),
    rate_limit_per_minute BIGINT NOT NULL DEFAULT 60,
    last_used_at DATETIME(6),
    created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_mcp_endpoints_tenant ON mcp_endpoints (tenant_id);

CREATE INDEX idx_mcp_endpoints_deleted ON mcp_endpoints (deleted_at);

-- 000023_message_artifacts_table.up.sql
CREATE TABLE IF NOT EXISTS message_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    position BIGINT NOT NULL,
    url LONGTEXT NOT NULL DEFAULT (''),
    file_name LONGTEXT NOT NULL DEFAULT (''),
    file_type VARCHAR(32) NOT NULL DEFAULT (''),
    file_size BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT (''),
    source_path LONGTEXT NOT NULL DEFAULT (''),
    mod_time VARCHAR(64) NOT NULL DEFAULT (''),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE UNIQUE INDEX idx_message_artifacts_message_position
    ON message_artifacts (message_id, position);

CREATE INDEX idx_message_artifacts_session_created
    ON message_artifacts (session_id, created_at);

-- 000024_message_context_checkpoint.up.sql
ALTER TABLE messages ADD COLUMN context_checkpoint JSON;

-- 000025_messages_session_created_index.up.sql
CREATE INDEX idx_messages_session_created_id
    ON messages (session_id, created_at DESC, id DESC);

-- 000026_message_artifacts_deleted_at.up.sql
ALTER TABLE message_artifacts ADD COLUMN deleted_at DATETIME(6);

CREATE INDEX idx_message_artifacts_session_live
    ON message_artifacts (session_id, created_at);

CREATE INDEX idx_message_artifacts_url_live
    ON message_artifacts (url(191));

-- 000027_session_sandbox_config_tenant.up.sql
ALTER TABLE sessions ADD COLUMN sandbox_config_tenant_id BIGINT NOT NULL DEFAULT 0;

-- 000028_tenant_skills_and_env_vars.up.sql
CREATE TABLE IF NOT EXISTS tenant_skills (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    catalog_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    description LONGTEXT,
    instructions LONGTEXT,
    bundle_ref VARCHAR(1024),
    bundle_sha256 VARCHAR(64),
    enabled BIGINT NOT NULL DEFAULT 1,
    installed_snapshot_id VARCHAR(255),
    install_session_id VARCHAR(36),
    install_message_id VARCHAR(36),
    envs JSON,
    served JSON,
    status VARCHAR(32) NOT NULL,
    error LONGTEXT,
    installing_since DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS tenant_skill_snapshots (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    skill_id VARCHAR(36),
    snapshot_id VARCHAR(255),
    parent_snapshot_id VARCHAR(255),
    generation BIGINT NOT NULL DEFAULT 0,
    planned_name VARCHAR(255),
    `trigger` VARCHAR(16) NOT NULL,
    state VARCHAR(16) NOT NULL,
    superseded_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS tenant_skill_catalog (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    description LONGTEXT,
    instructions LONGTEXT,
    bundle_ref VARCHAR(1024),
    bundle_sha256 VARCHAR(64),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE IF NOT EXISTS tenant_user_env_vars (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    principal_type VARCHAR(32) NOT NULL,
    principal_id VARCHAR(512) NOT NULL,
    sandbox_config_id VARCHAR(36) NOT NULL,
    skill_id VARCHAR(36) NOT NULL DEFAULT (''),
    name VARCHAR(255) NOT NULL,
    value LONGTEXT,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_tenant_skills_catalog
    ON tenant_skills (catalog_id);

CREATE INDEX idx_tenant_skill_snapshots_config
    ON tenant_skill_snapshots (sandbox_config_id);

CREATE INDEX idx_tenant_skill_snapshots_state
    ON tenant_skill_snapshots (state);

CREATE INDEX idx_user_env_var_skill
    ON tenant_user_env_vars (tenant_id, skill_id);

CREATE INDEX idx_user_env_var_config
    ON tenant_user_env_vars (tenant_id, sandbox_config_id);

-- 000029_session_host_workspace_dir.up.sql
ALTER TABLE sessions ADD COLUMN host_workspace_dir VARCHAR(1024);

-- 000030_im_channel_locale.up.sql
ALTER TABLE im_channels ADD COLUMN locale VARCHAR(16) NOT NULL DEFAULT ('');

-- 000031_model_catalog_config.up.sql
CREATE TABLE model_catalog_configs (
    id BIGINT PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 0,
    overlay JSON NOT NULL,
    history JSON NOT NULL,
    updated_by VARCHAR(36) NOT NULL DEFAULT (''),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

INSERT INTO model_catalog_configs (id, version, overlay, history) VALUES (1, 0, JSON_OBJECT('providers', JSON_OBJECT()), JSON_ARRAY());

-- 000032_chunk_images.up.sql
CREATE TABLE IF NOT EXISTS chunk_images (
    chunk_id VARCHAR(36) NOT NULL,
    image_index BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_type VARCHAR(20) NOT NULL DEFAULT (''),
    image_key LONGTEXT NOT NULL,
    url LONGTEXT NOT NULL DEFAULT (''),
    original_url LONGTEXT NOT NULL DEFAULT (''),
    caption LONGTEXT NOT NULL DEFAULT (''),
    ocr_text LONGTEXT NOT NULL DEFAULT (''),
    attrs JSON NOT NULL DEFAULT (JSON_OBJECT()),
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    status BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6),
    updated_at DATETIME(6),
    PRIMARY KEY (chunk_id, image_index)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_chunk_images_listing
    ON chunk_images(knowledge_base_id, tenant_id, created_at, chunk_id, image_index);

CREATE INDEX idx_chunk_images_copies
    ON chunk_images(knowledge_base_id, tenant_id, image_key(191), updated_at, chunk_id, image_index);

-- 000033_chunk_source_locators.up.sql
ALTER TABLE chunks ADD COLUMN source_locators JSON;

-- PostgreSQL 000092_mcp_metadata
CREATE TABLE IF NOT EXISTS mcp_metadata (
    tenant_id BIGINT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    principal VARCHAR(255) NOT NULL DEFAULT (''),
    config_fingerprint VARCHAR(64) NOT NULL,
    tools JSON NOT NULL,
    instructions LONGTEXT NOT NULL DEFAULT (''),
    server_name LONGTEXT NOT NULL DEFAULT (''),
    server_version LONGTEXT NOT NULL DEFAULT (''),
    server_description LONGTEXT NOT NULL DEFAULT (''),
    synced_at DATETIME(6) NOT NULL,
    PRIMARY KEY (tenant_id, service_id, principal)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

ALTER TABLE mcp_services ADD COLUMN usage_instructions LONGTEXT NOT NULL DEFAULT ('');

-- PostgreSQL 000081/000082: the SQLite base image includes artifacts, but the
-- older MySQL image did not. Sandbox configurations are standard-edition data.
ALTER TABLE messages ADD COLUMN artifacts JSON DEFAULT (JSON_ARRAY());

CREATE TABLE tenant_sandbox_configs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    sandbox_type VARCHAR(32) NOT NULL,
    config JSON NOT NULL,
    cordoned_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    active_row TINYINT(1) GENERATED ALWAYS AS
        (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE INDEX idx_tenant_sandbox_configs_tenant ON tenant_sandbox_configs(tenant_id, active_row);
CREATE UNIQUE INDEX uq_tenant_sandbox_configs_tenant_name
    ON tenant_sandbox_configs(tenant_id, name, active_row);

-- MySQL unique indexes treat NULL as distinct, preserving PostgreSQL partial
-- unique index scopes for live, nonempty values.
ALTER TABLE tenant_invitations MODIFY COLUMN pending_active TINYINT(1)
    GENERATED ALWAYS AS (CASE WHEN status = 'pending' AND deleted_at IS NULL
        AND invitee_user_id <> '' THEN 1 ELSE NULL END) STORED;
ALTER TABLE tenant_invitations ADD COLUMN active_token TINYINT(1)
    GENERATED ALWAYS AS (CASE WHEN token <> '' AND deleted_at IS NULL THEN 1 ELSE NULL END) STORED;
CREATE UNIQUE INDEX idx_tenant_invitations_token ON tenant_invitations(token, active_token);

DROP INDEX idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens;
CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_principal_svc
    ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);

ALTER TABLE mcp_endpoints ADD COLUMN active_token TINYINT(1)
    GENERATED ALWAYS AS (CASE WHEN token_hash <> '' AND deleted_at IS NULL THEN 1 ELSE NULL END) STORED;
CREATE UNIQUE INDEX idx_mcp_endpoints_token_hash ON mcp_endpoints(token_hash, active_token);

ALTER TABLE tenant_skills ADD COLUMN active_row TINYINT(1)
    GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED;
CREATE UNIQUE INDEX uq_tenant_skills_config_name ON tenant_skills(sandbox_config_id, name, active_row);
ALTER TABLE tenant_skill_catalog ADD COLUMN active_row TINYINT(1)
    GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED;
CREATE UNIQUE INDEX uq_tenant_skill_catalog_name ON tenant_skill_catalog(tenant_id, name, active_row);

-- A full composite key can exceed InnoDB's 3072-byte limit under utf8mb4.
-- JSON_ARRAY serializes every complete component with type/length boundaries;
-- SHA-256 keeps the B-tree key fixed size. A theoretical digest collision is
-- possible and is documented in the migration coverage report.
ALTER TABLE tenant_user_env_vars ADD COLUMN unique_key_digest BINARY(32)
    GENERATED ALWAYS AS (UNHEX(SHA2(CAST(JSON_ARRAY(tenant_id, principal_type,
        principal_id, sandbox_config_id, skill_id, name) AS CHAR CHARACTER SET utf8mb4), 256))) STORED;
CREATE UNIQUE INDEX uq_user_env_var ON tenant_user_env_vars(unique_key_digest);

ALTER TABLE memory_topic_stats ADD COLUMN unique_key_digest BINARY(32)
    GENERATED ALWAYS AS (UNHEX(SHA2(CAST(JSON_ARRAY(tenant_id, subject_id,
        normalized_key) AS CHAR CHARACTER SET utf8mb4), 256))) STORED;
CREATE UNIQUE INDEX idx_mem_topic_scope ON memory_topic_stats(unique_key_digest);
CREATE UNIQUE INDEX idx_mem_affinity_scope ON memory_doc_affinity(tenant_id, subject_id, knowledge_id);

DROP INDEX idx_knowledges_tag ON knowledges;
ALTER TABLE knowledges DROP COLUMN tag_id;
DROP INDEX idx_chunks_chunk_type ON chunks;
DROP INDEX idx_chunks_content_hash ON chunks;
DROP INDEX idx_chunks_tenant_kg ON chunks;

-- MySQL has no PostgreSQL GIN JSONB containment index; this B-tree narrows the
-- live user-message candidates before JSON_CONTAINS checks full attachments.
CREATE INDEX idx_messages_live_attachments ON messages(role, deleted_at, session_id);

-- Stable lock rows for short queue claim/drain transactions. Consumer work
-- happens after commit; independent lanes remain independent.
CREATE TABLE task_queue_lanes (
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    PRIMARY KEY (task_type, scope, scope_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

-- PostgreSQL 000012 sharing constraints use live rows only. NULL invite codes
-- remain unconstrained because MySQL permits multiple NULLs in a unique key.
CREATE UNIQUE INDEX idx_organizations_invite_code ON organizations(invite_code, active_row);
CREATE UNIQUE INDEX idx_kb_shares_unique ON kb_shares(knowledge_base_id, organization_id, active_row);
CREATE UNIQUE INDEX idx_agent_shares_unique ON agent_shares(agent_id, source_tenant_id, organization_id, active_row);

-- Explicit InnoDB foreign keys matching the surviving PostgreSQL constraints.
-- MySQL silently ignores column-level REFERENCES syntax.
ALTER TABLE users ADD CONSTRAINT fk_users_tenant_id
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL;
ALTER TABLE auth_tokens ADD CONSTRAINT fk_auth_tokens_user_id
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE organization_tenant_members ADD CONSTRAINT fk_organization_tenant_members_organization_id
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE kb_shares ADD CONSTRAINT fk_kb_shares_knowledge_base_id
    FOREIGN KEY (knowledge_base_id) REFERENCES knowledge_bases(id) ON DELETE CASCADE;
ALTER TABLE kb_shares ADD CONSTRAINT fk_kb_shares_organization_id
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE organization_join_requests ADD CONSTRAINT fk_organization_join_requests_organization_id
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE agent_shares ADD CONSTRAINT fk_agent_shares_organization_id
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE agent_shares ADD CONSTRAINT fk_agent_shares_agent_id
    FOREIGN KEY (agent_id, source_tenant_id) REFERENCES custom_agents(id, tenant_id) ON DELETE CASCADE;
ALTER TABLE im_channel_sessions ADD CONSTRAINT fk_im_channel_sessions_session_id
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;
ALTER TABLE sync_logs ADD CONSTRAINT fk_sync_logs_data_source_id
    FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE;
ALTER TABLE mcp_tool_approvals ADD CONSTRAINT fk_mcp_tool_approvals_service_id
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE;
ALTER TABLE mcp_oauth_clients ADD CONSTRAINT fk_mcp_oauth_clients_service_id
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE;
ALTER TABLE mcp_oauth_tokens ADD CONSTRAINT fk_mcp_oauth_tokens_service_id
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE;
ALTER TABLE tenant_api_keys ADD CONSTRAINT fk_tenant_api_keys_tenant_id
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE message_suggestion_sets ADD CONSTRAINT fk_message_suggestion_sets_tenant_id
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE message_suggestion_events ADD CONSTRAINT fk_message_suggestion_events_tenant_id
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE message_suggestion_events ADD CONSTRAINT fk_message_suggestion_events_suggestion_set_id
    FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE;
ALTER TABLE resource_bindings ADD CONSTRAINT fk_resource_bindings_resource_id
    FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE;
ALTER TABLE resource_access_grants ADD CONSTRAINT fk_resource_access_grants_resource_id
    FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE;
ALTER TABLE mcp_metadata ADD CONSTRAINT fk_mcp_metadata_service_id
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE;

-- Keep the image gallery projection in step with every chunk write.
-- Pin JSON text comparisons: JSON functions return utf8mb4_bin even when
-- the migration connection uses utf8mb4_0900_bin, which MySQL 8.4 cannot mix.
CREATE TRIGGER trg_chunk_images_insert AFTER INSERT ON chunks FOR EACH ROW
BEGIN
INSERT INTO chunk_images (chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id,
    chunk_type, image_key, url, original_url, caption, ocr_text, attrs,
    is_enabled, status, created_at, updated_at)
SELECT NEW.id, e.ord - 1, NEW.tenant_id, NEW.knowledge_base_id, NEW.knowledge_id,
    COALESCE(NEW.chunk_type, ''),
    COALESCE(NULLIF(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin END, ''),
        NULLIF(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin END, ''), CONCAT(NEW.id, '#', e.ord - 1)),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin END, ''),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin END, ''),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.caption')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.caption')) COLLATE utf8mb4_bin END, ''),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.ocr_text')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.ocr_text')) COLLATE utf8mb4_bin END, ''),
    CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.attrs.attrs')) COLLATE utf8mb4_bin = 'OBJECT'
         THEN JSON_EXTRACT(e.image, '$.attrs.attrs') ELSE JSON_OBJECT() END,
    NEW.is_enabled, COALESCE(NEW.status, 0), NEW.created_at, NEW.updated_at
FROM (
    SELECT j.ord, JSON_EXTRACT(NEW.image_info, CONCAT('$[', j.ord - 1, ']')) AS image
    FROM JSON_TABLE(CASE WHEN JSON_VALID(NEW.image_info) THEN
        CASE WHEN JSON_TYPE(NEW.image_info) COLLATE utf8mb4_bin = 'ARRAY' THEN NEW.image_info ELSE JSON_ARRAY() END
        ELSE JSON_ARRAY() END, '$[*]' COLUMNS (ord FOR ORDINALITY)) AS j
) AS e
WHERE NEW.deleted_at IS NULL AND JSON_TYPE(e.image) COLLATE utf8mb4_bin = 'OBJECT';
END;
CREATE TRIGGER trg_chunk_images_update AFTER UPDATE ON chunks FOR EACH ROW
BEGIN
DELETE FROM chunk_images WHERE chunk_id = OLD.id;
INSERT INTO chunk_images (chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id,
    chunk_type, image_key, url, original_url, caption, ocr_text, attrs,
    is_enabled, status, created_at, updated_at)
SELECT NEW.id, e.ord - 1, NEW.tenant_id, NEW.knowledge_base_id, NEW.knowledge_id,
    COALESCE(NEW.chunk_type, ''),
    COALESCE(NULLIF(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin END, ''),
        NULLIF(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin END, ''), CONCAT(NEW.id, '#', e.ord - 1)),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.url')) COLLATE utf8mb4_bin END, ''),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.original_url')) COLLATE utf8mb4_bin END, ''),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.caption')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.caption')) COLLATE utf8mb4_bin END, ''),
    COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.ocr_text')) COLLATE utf8mb4_bin = 'NULL' THEN NULL ELSE JSON_UNQUOTE(JSON_EXTRACT(e.image, '$.ocr_text')) COLLATE utf8mb4_bin END, ''),
    CASE WHEN JSON_TYPE(JSON_EXTRACT(e.image, '$.attrs.attrs')) COLLATE utf8mb4_bin = 'OBJECT'
         THEN JSON_EXTRACT(e.image, '$.attrs.attrs') ELSE JSON_OBJECT() END,
    NEW.is_enabled, COALESCE(NEW.status, 0), NEW.created_at, NEW.updated_at
FROM (
    SELECT j.ord, JSON_EXTRACT(NEW.image_info, CONCAT('$[', j.ord - 1, ']')) AS image
    FROM JSON_TABLE(CASE WHEN JSON_VALID(NEW.image_info) THEN
        CASE WHEN JSON_TYPE(NEW.image_info) COLLATE utf8mb4_bin = 'ARRAY' THEN NEW.image_info ELSE JSON_ARRAY() END
        ELSE JSON_ARRAY() END, '$[*]' COLUMNS (ord FOR ORDINALITY)) AS j
) AS e
WHERE NEW.deleted_at IS NULL AND JSON_TYPE(e.image) COLLATE utf8mb4_bin = 'OBJECT';
END;
CREATE TRIGGER trg_chunk_images_delete AFTER DELETE ON chunks FOR EACH ROW
DELETE FROM chunk_images WHERE chunk_id = OLD.id;

-- Final schema changes through PostgreSQL head 000133. This unpublished
-- baseline provisions fresh databases; historical row backfills are not replayed.
ALTER TABLE sessions ADD COLUMN sandbox_config_id VARCHAR(36) DEFAULT NULL;
ALTER TABLE sessions ADD COLUMN kind VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN owner_auth_source VARCHAR(32) NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_tenant_kind_user_updated
    ON sessions(tenant_id, kind, user_id, updated_at DESC, deleted_at);
ALTER TABLE tenants ADD COLUMN assistant_config JSON;
ALTER TABLE mcp_tool_approvals ADD COLUMN risk VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE mcp_tool_approvals ADD COLUMN observed_risk VARCHAR(16) NOT NULL DEFAULT '';
CREATE INDEX idx_resources_tenant_content_hash ON resources(tenant_id, content_hash, deleted_at);

CREATE TABLE IF NOT EXISTS agent_prompts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL DEFAULT (''),
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL DEFAULT (''),
    request_id VARCHAR(128) NOT NULL DEFAULT (''),
    kind VARCHAR(32) NOT NULL,
    tool_call_id VARCHAR(255) NOT NULL DEFAULT (''),
    suspendable TINYINT(1) NOT NULL DEFAULT false,
    request JSON,
    response JSON,
    status VARCHAR(32) NOT NULL,
    close_reason LONGTEXT NOT NULL DEFAULT (''),
    resolved_by VARCHAR(64) NOT NULL DEFAULT (''),
    resolved_at DATETIME(6),
    owner_instance VARCHAR(64) NOT NULL DEFAULT (''),
    lease_until DATETIME(6),
    expires_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_agent_prompts_session_status
    ON agent_prompts (session_id, status);
CREATE INDEX idx_agent_prompts_status_lease
    ON agent_prompts (status, lease_until);
CREATE INDEX idx_agent_prompts_status_expires
    ON agent_prompts (status, expires_at);
CREATE INDEX idx_agent_prompts_message
    ON agent_prompts (assistant_message_id);

CREATE TABLE IF NOT EXISTS agent_run_suspensions (
    assistant_message_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    prompt_id VARCHAR(36) NOT NULL,
    snapshot_version INT NOT NULL,
    snapshot JSON NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE INDEX idx_agent_run_suspensions_session
    ON agent_run_suspensions (session_id);

ALTER TABLE messages ADD COLUMN run_state VARCHAR(32) NOT NULL DEFAULT ('');

CREATE TABLE IF NOT EXISTS agent_collaborations (
 id VARCHAR(36) PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 session_id VARCHAR(36) NOT NULL,
 status VARCHAR(32) NOT NULL,
 version BIGINT NOT NULL,
 data LONGTEXT NOT NULL,
 active_parent TINYINT(1) GENERATED ALWAYS AS (CASE WHEN status IN ('active', 'cancelling') THEN 1 ELSE NULL END) STORED,
 updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE INDEX idx_agent_collaborations_status ON agent_collaborations(status, updated_at);
CREATE INDEX idx_agent_collaborations_owner ON agent_collaborations(tenant_id, user_id, session_id);
CREATE UNIQUE INDEX idx_agent_collaborations_active_parent ON agent_collaborations(tenant_id, user_id, session_id, active_parent);

CREATE TABLE IF NOT EXISTS agent_grants (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    scope VARCHAR(16) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    grant_key VARCHAR(512) NOT NULL,
    label LONGTEXT,
    binding_hash VARCHAR(64) NOT NULL DEFAULT (''),
    risk VARCHAR(16) NOT NULL DEFAULT ('write'),
    prompt_id VARCHAR(36),
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    last_used_at DATETIME(6),
    expires_at DATETIME(6),
    revoked_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE UNIQUE INDEX idx_agent_grants_key
    ON agent_grants(tenant_id, user_id, scope, scope_id, grant_key);
CREATE INDEX idx_agent_grants_scope ON agent_grants(tenant_id, scope, scope_id);

CREATE TABLE IF NOT EXISTS automations (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    enabled TINYINT(1) NOT NULL DEFAULT TRUE,
    model_id VARCHAR(64) NOT NULL DEFAULT (''),
    instruction LONGTEXT NOT NULL,
    trigger_config LONGTEXT NOT NULL,
    session_mode VARCHAR(16) NOT NULL DEFAULT ('per_run'),
    persistent_session_id VARCHAR(36) NOT NULL DEFAULT (''),
    carry_last_result TINYINT(1) NOT NULL DEFAULT FALSE,
    misfire_policy VARCHAR(16) NOT NULL DEFAULT ('skip'),
    overlap_policy VARCHAR(16) NOT NULL DEFAULT ('skip'),
    access_level VARCHAR(16) NOT NULL DEFAULT ('confirm'),
    approval_timeout_minutes INT NOT NULL DEFAULT 1440,
    limits LONGTEXT NOT NULL DEFAULT ('{}'),
    notify_on_success TINYINT(1) NOT NULL DEFAULT FALSE,
    next_run_at DATETIME(6),
    last_run_at DATETIME(6),
    last_success_at DATETIME(6),
    last_run_status VARCHAR(32) NOT NULL DEFAULT (''),
    consecutive_failures INT NOT NULL DEFAULT 0,
    paused_reason LONGTEXT NOT NULL DEFAULT (''),
    version BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    deleted_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE INDEX idx_automations_due ON automations(enabled, next_run_at);
CREATE INDEX idx_automations_owner ON automations(tenant_id, owner_user_id);
CREATE INDEX idx_automations_deleted_at ON automations(deleted_at);

CREATE TABLE IF NOT EXISTS automation_runs (
    id VARCHAR(36) PRIMARY KEY,
    automation_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    scheduled_at DATETIME(6) NOT NULL,
    execution_duration_ms BIGINT NOT NULL DEFAULT 0,
    trigger_kind VARCHAR(16) NOT NULL,
    status VARCHAR(32) NOT NULL,
    session_id VARCHAR(36) NOT NULL DEFAULT (''),
    user_message_id VARCHAR(36) NOT NULL DEFAULT (''),
    assistant_message_id VARCHAR(36) NOT NULL DEFAULT (''),
    owner VARCHAR(36) NOT NULL DEFAULT (''),
    lease_until DATETIME(6),
    pending_prompt_id VARCHAR(36) NOT NULL DEFAULT (''),
    resume TINYINT(1) NOT NULL DEFAULT FALSE,
    cancel_requested TINYINT(1) NOT NULL DEFAULT FALSE,
    answer LONGTEXT NOT NULL DEFAULT (''),
    error LONGTEXT NOT NULL DEFAULT (''),
    `usage` LONGTEXT,
    started_at DATETIME(6),
    finished_at DATETIME(6),
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE UNIQUE INDEX idx_automation_runs_slot
    ON automation_runs(automation_id, scheduled_at, trigger_kind);
CREATE INDEX idx_automation_runs_automation ON automation_runs(automation_id);
CREATE INDEX idx_automation_runs_status ON automation_runs(status);

CREATE TABLE IF NOT EXISTS notifications (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL,
    body LONGTEXT NOT NULL DEFAULT (''),
    link LONGTEXT NOT NULL DEFAULT (''),
    source_type VARCHAR(32) NOT NULL DEFAULT (''),
    source_id VARCHAR(36) NOT NULL DEFAULT (''),
    read_at DATETIME(6),
    created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE INDEX idx_notifications_user ON notifications(tenant_id, user_id, created_at);

CREATE TABLE IF NOT EXISTS session_sandbox_assignments (
    session_id        VARCHAR(36)  PRIMARY KEY,
    config_tenant_id  BIGINT       NOT NULL DEFAULT 0,
    config_id         VARCHAR(64)  NOT NULL DEFAULT (''),
    machine_digest    VARCHAR(64)  NOT NULL DEFAULT (''),
    released_at       DATETIME(6) NULL,
    cleanup_due_at    DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE INDEX idx_session_sandbox_assignments_live_machine
    ON session_sandbox_assignments (machine_digest, released_at);
CREATE INDEX idx_session_sandbox_assignments_cleanup_due
    ON session_sandbox_assignments (cleanup_due_at);

CREATE TABLE IF NOT EXISTS sandbox_user_machines (
    machine_digest    VARCHAR(64)  PRIMARY KEY,
    owner_tenant_id   BIGINT       NOT NULL,
    owner_id          VARCHAR(512) NOT NULL,
    config_tenant_id  BIGINT       NOT NULL,
    config_id         VARCHAR(64)  NOT NULL,
    empty_since       DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
CREATE INDEX idx_sandbox_user_machines_config ON sandbox_user_machines (config_tenant_id, config_id);
CREATE INDEX idx_sandbox_user_machines_owner ON sandbox_user_machines (owner_id, owner_tenant_id);
CREATE INDEX idx_sandbox_user_machines_empty ON sandbox_user_machines (empty_since);

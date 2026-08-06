-- MySQL schema for WeKnora (consolidated from PostgreSQL/SQLite migrations)

CREATE TABLE IF NOT EXISTS tenants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    retriever_engines LONGTEXT NOT NULL DEFAULT ('[]'),
    status VARCHAR(50) DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    agent_config LONGTEXT DEFAULT NULL,
    context_config LONGTEXT,
    conversation_config LONGTEXT,
    web_search_config LONGTEXT DEFAULT NULL,
    parser_engine_config LONGTEXT DEFAULT NULL,
    storage_engine_config LONGTEXT DEFAULT NULL,
    default_storage_backend_id VARCHAR(36),
    credentials LONGTEXT DEFAULT NULL,
    api_principal_config LONGTEXT DEFAULT NULL,
    chat_history_config LONGTEXT,
    retrieval_config LONGTEXT,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB AUTO_INCREMENT=10000 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description LONGTEXT,
    parameters LONGTEXT NOT NULL,
    is_default TINYINT(1) NOT NULL DEFAULT 0,
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    managed_by VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_models_type ON models(type);
CREATE INDEX idx_models_source ON models(source);
CREATE INDEX idx_models_is_builtin ON models(is_builtin);
CREATE INDEX idx_models_managed_by ON models(managed_by);

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    tenant_id INT NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config LONGTEXT NOT NULL DEFAULT ('{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}'),
    image_processing_config LONGTEXT NOT NULL DEFAULT ('{"enable_multimodal": false, "model_id": ""}'),
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config LONGTEXT NOT NULL DEFAULT ('{}'),
    storage_provider_config LONGTEXT DEFAULT NULL,
    vlm_config LONGTEXT NOT NULL DEFAULT ('{}'),
    extract_config LONGTEXT NULL DEFAULT NULL,
    faq_config LONGTEXT,
    question_generation_config LONGTEXT NULL,
    is_temporary TINYINT(1) NOT NULL DEFAULT 0,
    is_pinned INT NOT NULL DEFAULT 0,
    pinned_at DATETIME(3) NULL,
    asr_config LONGTEXT,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    creator_id VARCHAR(36),
    wiki_config LONGTEXT,
    indexing_strategy LONGTEXT DEFAULT ('{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX idx_knowledge_bases_tenant_vector_store
    ON knowledge_bases(tenant_id, vector_store_id);
CREATE INDEX idx_knowledge_bases_storage_backend
    ON knowledge_bases(tenant_id, storage_backend_id);
CREATE INDEX idx_knowledge_bases_tenant_creator
    ON knowledge_bases(tenant_id, creator_id);

CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
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
    metadata LONGTEXT,
    custom_metadata LONGTEXT NOT NULL DEFAULT ('{}'),
    tag_id VARCHAR(36),
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result LONGTEXT DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    processed_at DATETIME(3),
    error_message LONGTEXT,
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX idx_knowledges_enable_status ON knowledges(enable_status);
CREATE INDEX idx_knowledges_tag ON knowledges(tag_id);
CREATE INDEX idx_knowledges_summary_status ON knowledges(summary_status);

CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    title VARCHAR(255),
    description LONGTEXT,
    knowledge_base_id VARCHAR(36),
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite TINYINT(1) NOT NULL DEFAULT 1,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response LONGTEXT NOT NULL DEFAULT ('很抱歉，我暂时无法回答这个问题。'),
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INT NOT NULL DEFAULT 10,
    rerank_top_k INT NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters LONGTEXT NOT NULL DEFAULT ('{}'),
    agent_config LONGTEXT DEFAULT NULL,
    context_config LONGTEXT DEFAULT NULL,
    agent_id VARCHAR(36),
    user_id VARCHAR(512),
    is_pinned TINYINT(1) NOT NULL DEFAULT 0,
    pinned_at DATETIME(3),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);
CREATE INDEX idx_sessions_tenant_user_pin
    ON sessions (tenant_id, user_id, is_pinned, pinned_at, updated_at);

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content LONGTEXT NOT NULL,
    rendered_content LONGTEXT NOT NULL DEFAULT (''),
    knowledge_references LONGTEXT NOT NULL DEFAULT ('[]'),
    agent_steps LONGTEXT DEFAULT NULL,
    mentioned_items LONGTEXT DEFAULT ('[]'),
    images LONGTEXT DEFAULT ('[]'),
    attachments LONGTEXT DEFAULT ('[]'),
    is_completed TINYINT(1) NOT NULL DEFAULT 0,
    is_fallback TINYINT(1) NOT NULL DEFAULT 0,
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INT NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context LONGTEXT NOT NULL DEFAULT ('{}'),
    agent_duration_ms INT DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_messages_session_id ON messages(session_id);
CREATE INDEX idx_messages_knowledge_id ON messages(knowledge_id);
CREATE INDEX idx_messages_agent_id ON messages(agent_id);

CREATE TABLE IF NOT EXISTS message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INT NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    allow_regenerate TINYINT(1) NOT NULL DEFAULT 0,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions LONGTEXT NOT NULL DEFAULT ('[]'),
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    latency_ms INT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until DATETIME(3),
    generated_at DATETIME(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE UNIQUE INDEX idx_message_suggestion_sets_cache_key
    ON message_suggestion_sets(tenant_id, assistant_message_id, placement, config_hash, locale);
CREATE INDEX idx_message_suggestion_sets_session
    ON message_suggestion_sets(tenant_id, session_id, created_at);
CREATE INDEX idx_message_suggestion_sets_status
    ON message_suggestion_sets(status, lease_until);

CREATE TABLE IF NOT EXISTS message_suggestion_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_message_suggestion_events_set
    ON message_suggestion_events(suggestion_set_id, created_at);
CREATE INDEX idx_message_suggestion_events_session
    ON message_suggestion_events(tenant_id, session_id, created_at);
CREATE INDEX idx_message_suggestion_events_type
    ON message_suggestion_events(event_type, created_at);

CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content LONGTEXT NOT NULL,
    source_content LONGTEXT NOT NULL DEFAULT (''),
    content_revision INT NOT NULL DEFAULT 0,
    index_status VARCHAR(16) NOT NULL DEFAULT 'ready',
    last_editor_id VARCHAR(64) NOT NULL DEFAULT '',
    context_header LONGTEXT NOT NULL DEFAULT (''),
    chunk_index INT NOT NULL,
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    start_at INT NOT NULL,
    end_at INT NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info LONGTEXT,
    video_info LONGTEXT,
    relation_chunks LONGTEXT,
    indirect_relation_chunks LONGTEXT,
    metadata LONGTEXT,
    tag_id VARCHAR(36),
    status INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64),
    flags INT NOT NULL DEFAULT 1,
    seq_id INT,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
CREATE INDEX idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX idx_chunks_chunk_type ON chunks(chunk_type);
CREATE INDEX idx_chunks_tag ON chunks(tag_id);
CREATE INDEX idx_chunks_content_hash ON chunks(content_hash);
CREATE UNIQUE INDEX idx_chunks_seq_id ON chunks(seq_id);
CREATE INDEX idx_chunks_kb_tenant ON chunks(knowledge_base_id, tenant_id);
CREATE INDEX idx_chunks_knowledge_enabled ON chunks(knowledge_id, is_enabled, deleted_at);

CREATE TABLE IF NOT EXISTS chunk_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    revision INT NOT NULL,
    content LONGTEXT NOT NULL DEFAULT (''),
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    editor_id VARCHAR(64) NOT NULL DEFAULT '',
    edit_source VARCHAR(16) NOT NULL DEFAULT 'user',
    edited_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE(chunk_id, revision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_chunk_revisions_tenant_chunk ON chunk_revisions(tenant_id, chunk_id);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id INT,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    can_access_all_tenants TINYINT(1) NOT NULL DEFAULT 0,
    is_system_admin TINYINT(1) NOT NULL DEFAULT 0,
    -- Per-user JSON preferences (memory toggle, future UI knobs).
    -- SQLite has no JSONB; store as LONGTEXT and let GORM (de)serialise via
    -- the driver.Valuer / sql.Scanner methods on types.UserPreferences.
    preferences LONGTEXT NOT NULL DEFAULT ('{}'),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_is_system_admin ON users(is_system_admin);
CREATE INDEX idx_users_deleted_at ON users(deleted_at);

CREATE TABLE IF NOT EXISTS auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token LONGTEXT NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    is_revoked TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX idx_auth_tokens_token ON auth_tokens(token(191));
CREATE INDEX idx_auth_tokens_token_type ON auth_tokens(token_type);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

-- tenant_members carries the per-(user, tenant) TenantRole used by the
-- tenant-level RBAC introduced in #1303. SQLite does not support partial
-- indexes the same way Postgres does, so we use a plain unique index on
-- (user_id, tenant_id) — soft-deleted rows are filtered by the GORM scope.
CREATE TABLE IF NOT EXISTS tenant_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36),
    joined_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id);
CREATE INDEX idx_tenant_members_tenant_role
    ON tenant_members(tenant_id, role);
CREATE INDEX idx_tenant_members_user
    ON tenant_members(user_id);

-- audit_logs is the generic per-tenant durability for RBAC events
-- (and future KB / agent / datasource events). Sqlite mirror of the
-- 000044_audit_log migration; same column shape with INT for the
-- BIGSERIAL id and LONGTEXT in place of JSONB for details.
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NOT NULL,
    actor_user_id VARCHAR(36) NOT NULL DEFAULT '',
    actor_role VARCHAR(32) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL DEFAULT '',
    target_id VARCHAR(64) NOT NULL DEFAULT '',
    target_user_id VARCHAR(36) NOT NULL DEFAULT '',
    request_path VARCHAR(512) NOT NULL DEFAULT '',
    request_method VARCHAR(16) NOT NULL DEFAULT '',
    outcome VARCHAR(16) NOT NULL DEFAULT 'success',
    details LONGTEXT NOT NULL DEFAULT ('{}'),
    scope_type VARCHAR(32) NOT NULL DEFAULT '',
    scope_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
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
    tenant_id INT NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id VARCHAR(64) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
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
    tenant_id INT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (tenant_id, user_id, kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_user_kb_pins_user_tenant_pinned_at
    ON user_kb_pins(tenant_id, user_id, pinned_at DESC);

-- tenant_invitations — sqlite mirror of migration 000048. SQLite supports
-- partial unique indexes too, so the same "one pending per (tenant,
-- invitee)" guard can be applied verbatim.
CREATE TABLE IF NOT EXISTS tenant_invitations (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL,
    invited_by VARCHAR(36),
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    message VARCHAR(500),
    expires_at DATETIME(3) NOT NULL,
    responded_at DATETIME(3),
    token VARCHAR(64) NOT NULL DEFAULT '',
    accepted_count INT NOT NULL DEFAULT 0,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id);
CREATE INDEX idx_tenant_invitations_tenant
    ON tenant_invitations(tenant_id);
CREATE INDEX idx_tenant_invitations_invitee
    ON tenant_invitations(invitee_user_id);
CREATE INDEX idx_tenant_invitations_token ON tenant_invitations(token);

CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order INT NOT NULL DEFAULT 0,
    seq_id INT,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_knowledge_tags_kb_name ON knowledge_tags(tenant_id, knowledge_base_id, name);
CREATE INDEX idx_knowledge_tags_kb ON knowledge_tags(tenant_id, knowledge_base_id);
CREATE UNIQUE INDEX idx_knowledge_tags_seq_id ON knowledge_tags(seq_id);

CREATE TABLE IF NOT EXISTS mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
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
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_mcp_services_tenant_id ON mcp_services(tenant_id);
CREATE INDEX idx_mcp_services_enabled ON mcp_services(enabled);
CREATE INDEX idx_mcp_services_is_builtin ON mcp_services(is_builtin);
CREATE INDEX idx_mcp_services_deleted_at ON mcp_services(deleted_at);

CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_mcp_tool_approvals_tenant_svc_tool ON mcp_tool_approvals(tenant_id, service_id, tool_name);
CREATE INDEX idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    client_id VARCHAR(512) NOT NULL,
    client_secret LONGTEXT,
    redirect_uri VARCHAR(1024),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_mcp_oauth_clients_tenant_svc ON mcp_oauth_clients(tenant_id, service_id);
CREATE INDEX idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    user_id VARCHAR(512) NOT NULL,
    principal_type VARCHAR(32) NOT NULL DEFAULT 'web_user',
    principal_id VARCHAR(512) NOT NULL DEFAULT '',
    service_id VARCHAR(36) NOT NULL,
    access_token LONGTEXT,
    refresh_token LONGTEXT,
    token_type VARCHAR(32),
    expires_at DATETIME(3),
    refresh_lease_id VARCHAR(36),
    refresh_lease_until DATETIME(3),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_principal_svc ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);
CREATE INDEX idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);
CREATE INDEX idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);
CREATE INDEX idx_mcp_oauth_tokens_principal ON mcp_oauth_tokens(principal_type, principal_id);

CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    avatar VARCHAR(64),
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    tenant_id INT NOT NULL,
    created_by VARCHAR(36),
    runnable_by_viewer TINYINT(1) NOT NULL DEFAULT 1,
    config LONGTEXT NOT NULL DEFAULT ('{}'),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3),
    PRIMARY KEY (id, tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX idx_custom_agents_deleted_at ON custom_agents(deleted_at);

CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description LONGTEXT,
    owner_id VARCHAR(36) NOT NULL,
    -- Plan 3 (#1303): owning tenant pinned at create time; see migration 000046.
    owner_tenant_id INT NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval TINYINT(1) DEFAULT 0,
    invite_code_expires_at DATETIME(3),
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable TINYINT(1) NOT NULL DEFAULT 0,
    member_limit INT NOT NULL DEFAULT 50,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX idx_organizations_owner_tenant ON organizations(owner_tenant_id);
CREATE INDEX idx_organizations_deleted_at ON organizations(deleted_at);

CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at DATETIME(3),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_org_tenant_members_unique ON organization_tenant_members(organization_id, tenant_id);
CREATE INDEX idx_org_tenant_members_by_tenant ON organization_tenant_members(tenant_id);
CREATE INDEX idx_org_tenant_members_role ON organization_tenant_members(organization_id, role);

CREATE TABLE IF NOT EXISTS kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INT NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);
CREATE INDEX idx_kb_shares_org_id ON kb_shares(organization_id);
CREATE INDEX idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);
CREATE INDEX idx_kb_shares_deleted_at ON kb_shares(deleted_at);

CREATE TABLE IF NOT EXISTS organization_join_requests (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32),
    message LONGTEXT,
    reviewed_by VARCHAR(36),
    reviewed_at DATETIME(3),
    review_message LONGTEXT,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_org_join_requests_org_id ON organization_join_requests(organization_id);
CREATE INDEX idx_org_join_requests_user_id ON organization_join_requests(user_id);
CREATE INDEX idx_org_join_requests_status ON organization_join_requests(status);
-- Plan 3 (#1303): at most one pending request per (org, tenant, type).
-- Approved/rejected rows are not constrained so the audit trail stays.
CREATE INDEX uq_org_join_requests_pending_per_tenant
    ON organization_join_requests(organization_id, tenant_id, request_type);

CREATE TABLE IF NOT EXISTS agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INT NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_agent_shares_agent_id ON agent_shares(agent_id);
CREATE INDEX idx_agent_shares_org_id ON agent_shares(organization_id);
CREATE INDEX idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);
CREATE INDEX idx_agent_shares_deleted_at ON agent_shares(deleted_at);

CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_tenant_disabled_shared_agents_tenant_id ON tenant_disabled_shared_agents(tenant_id);

CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id VARCHAR(36) PRIMARY KEY,
    platform VARCHAR(20) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    chat_id VARCHAR(128) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    agent_id VARCHAR(36) DEFAULT '',
    im_channel_id VARCHAR(36) DEFAULT '',
    thread_id VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata LONGTEXT DEFAULT ('{}'),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id);
CREATE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id);
CREATE INDEX idx_im_channel_tenant ON im_channel_sessions (tenant_id);
CREATE INDEX idx_im_channel_session ON im_channel_sessions (session_id);
CREATE INDEX idx_im_channel_sessions_channel ON im_channel_sessions (im_channel_id);

CREATE TABLE IF NOT EXISTS im_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled INT NOT NULL DEFAULT 1,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    credentials LONGTEXT NOT NULL DEFAULT ('{}'),
    knowledge_base_id VARCHAR(36) DEFAULT '',
    bot_identity VARCHAR(255) NOT NULL DEFAULT '',
    session_mode VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_im_channels_tenant ON im_channels (tenant_id);
CREATE INDEX idx_im_channels_agent ON im_channels (agent_id);
CREATE INDEX idx_im_channels_bot_identity
    ON im_channels (bot_identity);

CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled INT NOT NULL DEFAULT 1,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins LONGTEXT NOT NULL DEFAULT ('[]'),
    welcome_message LONGTEXT NOT NULL DEFAULT (''),
    rate_limit_per_minute INT NOT NULL DEFAULT 30,
    rate_limit_per_day INT NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions INT NOT NULL DEFAULT 1,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search INT NOT NULL DEFAULT 0,
    allow_file_upload INT NOT NULL DEFAULT 0,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_embed_channels_tenant ON embed_channels (tenant_id);
CREATE INDEX idx_embed_channels_agent ON embed_channels (agent_id);
CREATE INDEX idx_embed_channels_publish_token
    ON embed_channels (publish_token);

CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config LONGTEXT,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT 'incremental',
    status VARCHAR(32) DEFAULT 'active',
    conflict_strategy VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions INT DEFAULT 1,
    last_sync_at DATETIME(3) NULL,
    last_sync_cursor LONGTEXT,
    last_sync_result LONGTEXT,
    error_message LONGTEXT,
    sync_log_retention_days INT DEFAULT 30,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_data_sources_tenant_id ON data_sources (tenant_id);
CREATE INDEX idx_data_sources_knowledge_base_id ON data_sources (knowledge_base_id);
CREATE INDEX idx_data_sources_type ON data_sources (type);
CREATE INDEX idx_data_sources_status ON data_sources (status);
CREATE INDEX idx_data_sources_deleted_at ON data_sources (deleted_at);

CREATE TABLE IF NOT EXISTS sync_logs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    finished_at DATETIME(3) NULL,
    items_total INT DEFAULT 0,
    items_created INT DEFAULT 0,
    items_updated INT DEFAULT 0,
    items_deleted INT DEFAULT 0,
    items_skipped INT DEFAULT 0,
    items_failed INT DEFAULT 0,
    error_message LONGTEXT,
    result LONGTEXT,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_sync_logs_data_source_id ON sync_logs (data_source_id);
CREATE INDEX idx_sync_logs_tenant_id ON sync_logs (tenant_id);
CREATE INDEX idx_sync_logs_status ON sync_logs (status);
CREATE INDEX idx_sync_logs_started_at ON sync_logs (started_at);

CREATE TABLE IF NOT EXISTS web_search_providers (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description LONGTEXT,
    parameters LONGTEXT,
    is_default INT DEFAULT 0,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_web_search_providers_tenant_id ON web_search_providers (tenant_id);
CREATE INDEX idx_web_search_providers_provider ON web_search_providers (provider);
CREATE INDEX idx_web_search_providers_deleted_at ON web_search_providers (deleted_at);

CREATE TABLE IF NOT EXISTS vector_stores (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config LONGTEXT NOT NULL DEFAULT ('{}'),
    index_config LONGTEXT NOT NULL DEFAULT ('{}'),
    tenant_id INT NOT NULL,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_vector_stores_name_tenant
    ON vector_stores(name, tenant_id);
CREATE INDEX idx_vector_stores_tenant_id ON vector_stores(tenant_id);
CREATE INDEX idx_vector_stores_engine_type ON vector_stores(engine_type);
CREATE INDEX idx_vector_stores_deleted_at ON vector_stores(deleted_at);

CREATE TABLE IF NOT EXISTS storage_backends (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config LONGTEXT NOT NULL DEFAULT ('{}'),
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_storage_backends_name_tenant
    ON storage_backends(tenant_id, name);
CREATE INDEX idx_storage_backends_legacy_alias
    ON storage_backends(tenant_id, provider, legacy_alias);
CREATE INDEX idx_storage_backends_tenant ON storage_backends(tenant_id);

CREATE TABLE IF NOT EXISTS resources (
    id VARCHAR(36) PRIMARY KEY,
    handle VARCHAR(22) NOT NULL UNIQUE,
    tenant_id INT NOT NULL,
    storage_backend_id VARCHAR(36),
    provider VARCHAR(32) NOT NULL,
    physical_path LONGTEXT NOT NULL,
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'file',
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    original_name VARCHAR(1024) NOT NULL DEFAULT '',
    size INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    lifecycle VARCHAR(16) NOT NULL DEFAULT 'persistent',
    expires_at DATETIME(3),
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_resources_tenant_location
    ON resources(tenant_id, location_hash);
CREATE INDEX idx_resources_tenant ON resources(tenant_id);
CREATE INDEX idx_resources_backend ON resources(storage_backend_id);

CREATE TABLE IF NOT EXISTS resource_bindings (
    id VARCHAR(36) PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE UNIQUE INDEX idx_resource_bindings_unique
    ON resource_bindings(resource_id, owner_type, owner_id, relation);
CREATE INDEX idx_resource_bindings_owner
    ON resource_bindings(tenant_id, owner_type, owner_id);

CREATE TABLE IF NOT EXISTS resource_access_grants (
    id VARCHAR(36) PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    resource_id VARCHAR(36) NOT NULL,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at DATETIME(3) NOT NULL,
    revoked_at DATETIME(3),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_resource_access_grants_resource
    ON resource_access_grants(resource_id);
CREATE INDEX idx_resource_access_grants_expires
    ON resource_access_grants(expires_at);

CREATE TABLE IF NOT EXISTS tenant_api_keys (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT,
    scope_type VARCHAR(16) NOT NULL DEFAULT 'tenant'
        CHECK (scope_type IN ('tenant', 'platform')),
    name VARCHAR(128) NOT NULL,
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    api_key LONGTEXT NOT NULL DEFAULT (''),
    full_access TINYINT(1) NOT NULL DEFAULT 0,
    knowledge_base_ids LONGTEXT NOT NULL DEFAULT ('[]'),
    capabilities LONGTEXT NOT NULL DEFAULT ('[]'),
    last_used_at DATETIME(3),
    expires_at DATETIME(3),
    revoked_at DATETIME(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = 0)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);
CREATE INDEX idx_tenant_api_keys_scope_type ON tenant_api_keys(scope_type);

CREATE TABLE IF NOT EXISTS temporary_documents (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref LONGTEXT NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size INT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content LONGTEXT NOT NULL DEFAULT (''),
    chunks LONGTEXT NOT NULL DEFAULT ('[]'),
    image_refs LONGTEXT NOT NULL DEFAULT ('[]'),
    metadata LONGTEXT NOT NULL DEFAULT ('{}'),
    processing_options LONGTEXT NOT NULL DEFAULT ('{}'),
    token_count INT NOT NULL DEFAULT 0,
    chunk_count INT NOT NULL DEFAULT 0,
    error_message LONGTEXT NOT NULL DEFAULT (''),
    expires_at DATETIME(3) NOT NULL,
    started_at DATETIME(3),
    ready_at DATETIME(3),
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_temporary_documents_scope ON temporary_documents(tenant_id, session_id);
CREATE INDEX idx_temporary_documents_status ON temporary_documents(status);
CREATE INDEX idx_temporary_documents_expires ON temporary_documents(expires_at);

-- ---------------------------------------------------------------------------
-- Wiki (consolidated from Postgres migrations 000037, 000040, 000061, 000075)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS wiki_pages (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug              VARCHAR(255) NOT NULL,
    title             VARCHAR(512) NOT NULL DEFAULT '',
    page_type         VARCHAR(32) NOT NULL DEFAULT 'summary',
    status            VARCHAR(32) NOT NULL DEFAULT 'published',
    content           LONGTEXT NOT NULL DEFAULT (''),
    summary           LONGTEXT NOT NULL DEFAULT (''),
    parent_slug       VARCHAR(255) NOT NULL DEFAULT '',
    folder_id         VARCHAR(36) NOT NULL DEFAULT '',
    category_path     LONGTEXT DEFAULT ('[]'),
    wiki_path         VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    sort_order        INT NOT NULL DEFAULT 0,
    source_refs       LONGTEXT DEFAULT ('[]'),
    chunk_refs        LONGTEXT DEFAULT ('[]'),
    in_links          LONGTEXT DEFAULT ('[]'),
    out_links         LONGTEXT DEFAULT ('[]'),
    page_metadata     LONGTEXT DEFAULT ('{}'),
    aliases           LONGTEXT DEFAULT ('[]'),
    version           INT NOT NULL DEFAULT 1,
    last_edit_source  VARCHAR(16) NOT NULL DEFAULT '',
    last_editor_id    VARCHAR(64) NOT NULL DEFAULT '',
    created_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at        DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_wiki_pages_kb_slug
    ON wiki_pages (knowledge_base_id, slug);

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
    tenant_id         INT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    sort_order        INT NOT NULL DEFAULT 0,
    created_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at        DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_wiki_folders_parent_name
    ON wiki_folders (knowledge_base_id, parent_id, name);

CREATE INDEX idx_wiki_folders_parent
    ON wiki_folders (knowledge_base_id, parent_id);

CREATE INDEX idx_wiki_folders_deleted_at
    ON wiki_folders (deleted_at);

CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id                    VARCHAR(36) PRIMARY KEY,
    tenant_id             INT NOT NULL,
    knowledge_base_id     VARCHAR(36) NOT NULL,
    slug                  VARCHAR(255) NOT NULL,
    issue_type            VARCHAR(50) NOT NULL,
    description           LONGTEXT NOT NULL,
    suspected_knowledge_ids LONGTEXT,
    status                VARCHAR(20) NOT NULL DEFAULT 'pending',
    reported_by           VARCHAR(100) NOT NULL,
    created_at            DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at            DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at            DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

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
    tenant_id         INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id           VARCHAR(36) NOT NULL,
    slug              VARCHAR(255) NOT NULL,
    version           INT NOT NULL,
    title             VARCHAR(512) NOT NULL DEFAULT '',
    page_type         VARCHAR(32) NOT NULL DEFAULT 'summary',
    status            VARCHAR(32) NOT NULL DEFAULT 'published',
    content           LONGTEXT NOT NULL DEFAULT (''),
    summary           LONGTEXT NOT NULL DEFAULT (''),
    aliases           LONGTEXT DEFAULT ('[]'),
    edit_source       VARCHAR(16) NOT NULL DEFAULT '',
    editor_id         VARCHAR(64) NOT NULL DEFAULT '',
    edited_at         DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    created_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_wiki_page_revisions_page_version
    ON wiki_page_revisions (page_id, version);

CREATE INDEX idx_wiki_page_revisions_kb_slug
    ON wiki_page_revisions (knowledge_base_id, slug);


-- Additional tables/relations added after the SQLite consolidated baseline.
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload LONGTEXT NOT NULL DEFAULT ('{}'),
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    claimed_at DATETIME(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_task_pending_ops_scope ON task_pending_ops(task_type, scope, scope_id, id);
CREATE INDEX idx_task_pending_ops_tenant ON task_pending_ops(tenant_id);

CREATE TABLE IF NOT EXISTS task_dead_letters (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    payload LONGTEXT NOT NULL,
    last_error LONGTEXT NOT NULL,
    fail_count INT NOT NULL,
    failed_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_task_dead_letters_scope ON task_dead_letters(scope, scope_id, failed_at DESC);
CREATE INDEX idx_task_dead_letters_tenant ON task_dead_letters(tenant_id, failed_at DESC);
CREATE INDEX idx_task_dead_letters_task_type ON task_dead_letters(task_type, failed_at DESC);

CREATE TABLE IF NOT EXISTS system_settings (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    `key` VARCHAR(128) NOT NULL UNIQUE,
    value LONGTEXT NOT NULL,
    value_type VARCHAR(16) NOT NULL,
    category VARCHAR(32) NOT NULL,
    description LONGTEXT NOT NULL DEFAULT (''),
    is_secret TINYINT(1) NOT NULL DEFAULT 0,
    requires_restart TINYINT(1) NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_system_settings_category ON system_settings(category);

CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    knowledge_id VARCHAR(36) NOT NULL,
    attempt INT NOT NULL DEFAULT 1,
    span_id VARCHAR(64) NOT NULL,
    parent_span_id VARCHAR(64) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    input LONGTEXT,
    output LONGTEXT,
    metadata LONGTEXT,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_message LONGTEXT NOT NULL DEFAULT (''),
    error_detail LONGTEXT NOT NULL DEFAULT (''),
    started_at DATETIME(3),
    finished_at DATETIME(3),
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY idx_kpspan_unique (knowledge_id, attempt, span_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_kpspan_knowledge_attempt ON knowledge_processing_spans(knowledge_id, attempt);
CREATE INDEX idx_kpspan_status_started ON knowledge_processing_spans(status, started_at);
CREATE INDEX idx_kpspan_parent ON knowledge_processing_spans(parent_span_id);

CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (knowledge_id, tag_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX idx_ktr_knowledge ON knowledge_tag_relations(knowledge_id);
CREATE INDEX idx_ktr_tag ON knowledge_tag_relations(tag_id);

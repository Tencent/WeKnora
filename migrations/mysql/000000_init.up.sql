-- MySQL schema for WeKnora (consolidated from all Postgres migrations)
-- MySQL notes:
--   - JSON type is used instead of PostgreSQL JSONB (MySQL 5.7+)
--   - Partial unique indexes (WHERE deleted_at IS NULL) are not supported in MySQL;
--     instead we rely on application-level GORM scope filtering.
--   - Default values for JSON columns use expressions (MySQL 8.0.13+).
--   - AUTO_INCREMENT replaces SERIAL/BIGSERIAL.

SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS knowledge_bases;
DROP TABLE IF EXISTS knowledges;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS message_suggestion_events;
DROP TABLE IF EXISTS message_suggestion_sets;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS tenant_members;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS user_resource_favorites;
DROP TABLE IF EXISTS user_kb_pins;
DROP TABLE IF EXISTS tenant_invitations;
DROP TABLE IF EXISTS knowledge_tags;
DROP TABLE IF EXISTS mcp_tool_approvals;
DROP TABLE IF EXISTS mcp_oauth_tokens;
DROP TABLE IF EXISTS mcp_oauth_clients;
DROP TABLE IF EXISTS mcp_services;
DROP TABLE IF EXISTS custom_agents;
DROP TABLE IF EXISTS agent_shares;
DROP TABLE IF EXISTS tenant_disabled_shared_agents;
DROP TABLE IF EXISTS kb_shares;
DROP TABLE IF EXISTS organization_join_requests;
DROP TABLE IF EXISTS organization_tenant_members;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS im_channel_sessions;
DROP TABLE IF EXISTS im_channels;
DROP TABLE IF EXISTS embed_channels;
DROP TABLE IF EXISTS data_sources;
DROP TABLE IF EXISTS sync_logs;
DROP TABLE IF EXISTS web_search_providers;
DROP TABLE IF EXISTS vector_stores;
DROP TABLE IF EXISTS storage_backends;
DROP TABLE IF EXISTS resource_bindings;
DROP TABLE IF EXISTS resource_access_grants;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS tenant_api_keys;
DROP TABLE IF EXISTS temporary_documents;
DROP TABLE IF EXISTS wiki_log_entries;
DROP TABLE IF EXISTS wiki_pages;
DROP TABLE IF EXISTS knowledge_processing_spans;
DROP TABLE IF EXISTS task_pending_ops;
DROP TABLE IF EXISTS system_settings;
SET FOREIGN_KEY_CHECKS = 1;

CREATE TABLE tenants (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    retriever_engines JSON NOT NULL DEFAULT ('[]'),
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
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=10000;

CREATE INDEX idx_tenants_status ON tenants(status);

CREATE TABLE models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON NOT NULL,
    is_default TINYINT(1) NOT NULL DEFAULT 0,
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    managed_by VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_models_type ON models(type);
CREATE INDEX idx_models_source ON models(source);
CREATE INDEX idx_models_is_builtin ON models(is_builtin);
CREATE INDEX idx_models_managed_by ON models(managed_by);

CREATE TABLE knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INT NOT NULL,
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
    is_temporary TINYINT(1) NOT NULL DEFAULT 0,
    is_pinned TINYINT(1) NOT NULL DEFAULT 0,
    pinned_at TIMESTAMP NULL DEFAULT NULL,
    asr_config JSON DEFAULT NULL,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    creator_id VARCHAR(36),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX idx_knowledge_bases_tenant_vector_store ON knowledge_bases(tenant_id, vector_store_id);
CREATE INDEX idx_knowledge_bases_storage_backend ON knowledge_bases(tenant_id, storage_backend_id);
CREATE INDEX idx_knowledge_bases_tenant_creator ON knowledge_bases(tenant_id, creator_id);

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
    metadata JSON DEFAULT NULL,
    tag_id VARCHAR(36),
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result JSON DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    processed_at TIMESTAMP NULL DEFAULT NULL,
    error_message TEXT,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX idx_knowledges_enable_status ON knowledges(enable_status);
CREATE INDEX idx_knowledges_tag ON knowledges(tag_id);
CREATE INDEX idx_knowledges_summary_status ON knowledges(summary_status);

CREATE TABLE sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    title VARCHAR(255),
    description TEXT,
    knowledge_base_id VARCHAR(36),
    max_rounds INT NOT NULL DEFAULT 5,
    enable_rewrite TINYINT(1) NOT NULL DEFAULT 1,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response TEXT NOT NULL DEFAULT ('很抱歉，我暂时无法回答这个问题。'),
    keyword_threshold DOUBLE NOT NULL DEFAULT 0.5,
    vector_threshold DOUBLE NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INT NOT NULL DEFAULT 10,
    rerank_top_k INT NOT NULL DEFAULT 10,
    rerank_threshold DOUBLE NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters JSON NOT NULL DEFAULT ('{}'),
    agent_config JSON DEFAULT NULL,
    context_config JSON DEFAULT NULL,
    agent_id VARCHAR(36),
    user_id VARCHAR(512),
    is_pinned TINYINT(1) NOT NULL DEFAULT 0,
    pinned_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);
CREATE INDEX idx_sessions_tenant_user_pin ON sessions (tenant_id, user_id, is_pinned, pinned_at, updated_at);

CREATE TABLE messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    rendered_content TEXT NOT NULL DEFAULT (''),
    knowledge_references JSON NOT NULL DEFAULT ('[]'),
    agent_steps JSON DEFAULT NULL,
    mentioned_items JSON DEFAULT ('[]'),
    images JSON DEFAULT ('[]'),
    is_completed TINYINT(1) NOT NULL DEFAULT 0,
    is_fallback TINYINT(1) NOT NULL DEFAULT 0,
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INT NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context JSON NOT NULL DEFAULT ('{}'),
    agent_duration_ms INT DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_messages_session_id ON messages(session_id);
CREATE INDEX idx_messages_knowledge_id ON messages(knowledge_id);
CREATE INDEX idx_messages_agent_id ON messages(agent_id);

CREATE TABLE message_suggestion_sets (
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
    questions JSON NOT NULL DEFAULT ('[]'),
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    latency_ms INT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMP NULL DEFAULT NULL,
    generated_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_message_suggestion_sets_cache_key
    ON message_suggestion_sets(tenant_id, assistant_message_id, placement, config_hash, locale);
CREATE INDEX idx_message_suggestion_sets_session
    ON message_suggestion_sets(tenant_id, session_id, created_at);
CREATE INDEX idx_message_suggestion_sets_status
    ON message_suggestion_sets(status, lease_until);

CREATE TABLE message_suggestion_events (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_message_suggestion_events_set
        FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_message_suggestion_events_set ON message_suggestion_events(suggestion_set_id, created_at);
CREATE INDEX idx_message_suggestion_events_session ON message_suggestion_events(tenant_id, session_id, created_at);
CREATE INDEX idx_message_suggestion_events_type ON message_suggestion_events(event_type, created_at);

CREATE TABLE chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    chunk_index INT NOT NULL,
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    start_at INT NOT NULL,
    end_at INT NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info TEXT,
    video_info TEXT,
    relation_chunks JSON DEFAULT NULL,
    indirect_relation_chunks JSON DEFAULT NULL,
    metadata JSON DEFAULT NULL,
    tag_id VARCHAR(36),
    status INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64),
    flags INT NOT NULL DEFAULT 1,
    seq_id INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
CREATE INDEX idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX idx_chunks_chunk_type ON chunks(chunk_type);
CREATE INDEX idx_chunks_tag ON chunks(tag_id);
CREATE INDEX idx_chunks_content_hash ON chunks(content_hash);
CREATE UNIQUE INDEX idx_chunks_seq_id ON chunks(seq_id);
CREATE INDEX idx_chunks_kb_tenant ON chunks(knowledge_base_id, tenant_id);
CREATE INDEX idx_chunks_knowledge_enabled ON chunks(knowledge_id, is_enabled, deleted_at);

CREATE TABLE users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id INT,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    can_access_all_tenants TINYINT(1) NOT NULL DEFAULT 0,
    preferences JSON NOT NULL DEFAULT ('{}'),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

CREATE UNIQUE INDEX idx_users_username ON users(username);
CREATE UNIQUE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_deleted_at ON users(deleted_at);

CREATE TABLE auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token TEXT NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    is_revoked TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX idx_auth_tokens_token ON auth_tokens(token(255));
CREATE INDEX idx_auth_tokens_token_type ON auth_tokens(token_type);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

CREATE TABLE tenant_members (
    id INT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36),
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique ON tenant_members(user_id, tenant_id);
CREATE INDEX idx_tenant_members_tenant_role ON tenant_members(tenant_id, role);
CREATE INDEX idx_tenant_members_user ON tenant_members(user_id);

CREATE TABLE audit_logs (
    id INT AUTO_INCREMENT PRIMARY KEY,
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
    details JSON NOT NULL DEFAULT ('{}'),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_audit_logs_tenant_id_desc ON audit_logs(tenant_id, id);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_user_id);
CREATE INDEX idx_audit_logs_tenant_action ON audit_logs(tenant_id, action);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);

CREATE TABLE user_resource_favorites (
    user_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_user_resource_favorites_user_tenant_time
    ON user_resource_favorites(user_id, tenant_id, resource_type, created_at);
CREATE INDEX idx_user_resource_favorites_tenant_id ON user_resource_favorites(tenant_id);

CREATE TABLE user_kb_pins (
    tenant_id INT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_user_kb_pins_user_tenant_pinned_at ON user_kb_pins(tenant_id, user_id, pinned_at);

CREATE TABLE tenant_invitations (
    id INT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL,
    invited_by VARCHAR(36),
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    message VARCHAR(500),
    expires_at TIMESTAMP NOT NULL,
    responded_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Note: partial unique index (WHERE status = 'pending' AND deleted_at IS NULL) not natively
-- supported in MySQL. Application-level GORM scope enforces this constraint.
CREATE INDEX idx_tenant_invitations_tenant ON tenant_invitations(tenant_id);
CREATE INDEX idx_tenant_invitations_invitee ON tenant_invitations(invitee_user_id);

CREATE TABLE knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order INT NOT NULL DEFAULT 0,
    seq_id INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_knowledge_tags_kb_name ON knowledge_tags(tenant_id, knowledge_base_id, name);
CREATE INDEX idx_knowledge_tags_kb ON knowledge_tags(tenant_id, knowledge_base_id);
CREATE UNIQUE INDEX idx_knowledge_tags_seq_id ON knowledge_tags(seq_id);

CREATE TABLE mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled TINYINT(1) DEFAULT 1,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512),
    headers JSON DEFAULT NULL,
    auth_config JSON DEFAULT NULL,
    advanced_config JSON DEFAULT NULL,
    stdio_config JSON DEFAULT NULL,
    env_vars JSON DEFAULT NULL,
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_mcp_services_tenant_id ON mcp_services(tenant_id);
CREATE INDEX idx_mcp_services_enabled ON mcp_services(enabled);
CREATE INDEX idx_mcp_services_is_builtin ON mcp_services(is_builtin);
CREATE INDEX idx_mcp_services_deleted_at ON mcp_services(deleted_at);

CREATE TABLE mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_mcp_tool_approvals_service FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_mcp_tool_approvals_tenant_svc_tool ON mcp_tool_approvals(tenant_id, service_id, tool_name);
CREATE INDEX idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);

CREATE TABLE mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    client_id VARCHAR(512) NOT NULL,
    client_secret TEXT,
    redirect_uri VARCHAR(1024),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_mcp_oauth_clients_service FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_mcp_oauth_clients_tenant_svc ON mcp_oauth_clients(tenant_id, service_id);
CREATE INDEX idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);

CREATE TABLE mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32),
    expires_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_mcp_oauth_tokens_service FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens(tenant_id, user_id, service_id);
CREATE INDEX idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);
CREATE INDEX idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);

CREATE TABLE custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    tenant_id INT NOT NULL,
    created_by VARCHAR(36),
    runnable_by_viewer TINYINT(1) NOT NULL DEFAULT 1,
    config JSON NOT NULL DEFAULT ('{}'),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (id, tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX idx_custom_agents_deleted_at ON custom_agents(deleted_at);

CREATE TABLE organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id INT NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval TINYINT(1) DEFAULT 0,
    invite_code_expires_at TIMESTAMP NULL DEFAULT NULL,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable TINYINT(1) NOT NULL DEFAULT 0,
    member_limit INT NOT NULL DEFAULT 50,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX idx_organizations_owner_tenant ON organizations(owner_tenant_id);
CREATE INDEX idx_organizations_deleted_at ON organizations(deleted_at);

CREATE TABLE organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_org_tenant_members_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_org_tenant_members_unique ON organization_tenant_members(organization_id, tenant_id);
CREATE INDEX idx_org_tenant_members_by_tenant ON organization_tenant_members(tenant_id);
CREATE INDEX idx_org_tenant_members_role ON organization_tenant_members(organization_id, role);

CREATE TABLE kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INT NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    CONSTRAINT fk_kb_shares_kb FOREIGN KEY (knowledge_base_id) REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    CONSTRAINT fk_kb_shares_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);
CREATE INDEX idx_kb_shares_org_id ON kb_shares(organization_id);
CREATE INDEX idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);
CREATE INDEX idx_kb_shares_deleted_at ON kb_shares(deleted_at);

CREATE TABLE organization_join_requests (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32),
    message TEXT,
    reviewed_by VARCHAR(36),
    reviewed_at TIMESTAMP NULL DEFAULT NULL,
    review_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_org_join_requests_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_org_join_requests_org_id ON organization_join_requests(organization_id);
CREATE INDEX idx_org_join_requests_user_id ON organization_join_requests(user_id);
CREATE INDEX idx_org_join_requests_status ON organization_join_requests(status);

CREATE TABLE agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INT NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    CONSTRAINT fk_agent_shares_org FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_agent_shares_agent_id ON agent_shares(agent_id);
CREATE INDEX idx_agent_shares_org_id ON agent_shares(organization_id);
CREATE INDEX idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);
CREATE INDEX idx_agent_shares_deleted_at ON agent_shares(deleted_at);

CREATE TABLE tenant_disabled_shared_agents (
    tenant_id INT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id INT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_tenant_disabled_shared_agents_tenant ON tenant_disabled_shared_agents(tenant_id);

CREATE TABLE im_channel_sessions (
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
    metadata JSON DEFAULT ('{}'),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    CONSTRAINT fk_im_channel_sessions_session FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_channel_lookup ON im_channel_sessions (platform, user_id, chat_id, tenant_id);
CREATE UNIQUE INDEX idx_channel_thread_lookup ON im_channel_sessions (platform, chat_id, thread_id, tenant_id);
CREATE INDEX idx_im_channel_tenant ON im_channel_sessions (tenant_id);
CREATE INDEX idx_im_channel_session ON im_channel_sessions (session_id);
CREATE INDEX idx_im_channel_sessions_channel ON im_channel_sessions (im_channel_id);

CREATE TABLE im_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    credentials JSON NOT NULL DEFAULT ('{}'),
    knowledge_base_id VARCHAR(36) DEFAULT '',
    bot_identity VARCHAR(255) NOT NULL DEFAULT '',
    session_mode VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_im_channels_tenant ON im_channels (tenant_id);
CREATE INDEX idx_im_channels_agent ON im_channels (agent_id);
CREATE UNIQUE INDEX idx_im_channels_bot_identity ON im_channels (bot_identity);

CREATE TABLE embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSON NOT NULL DEFAULT ('[]'),
    welcome_message TEXT NOT NULL DEFAULT (''),
    rate_limit_per_minute INT NOT NULL DEFAULT 30,
    rate_limit_per_day INT NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions TINYINT(1) NOT NULL DEFAULT 1,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search TINYINT(1) NOT NULL DEFAULT 0,
    allow_file_upload TINYINT(1) NOT NULL DEFAULT 0,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_embed_channels_tenant ON embed_channels (tenant_id);
CREATE INDEX idx_embed_channels_agent ON embed_channels (agent_id);
CREATE UNIQUE INDEX idx_embed_channels_publish_token ON embed_channels (publish_token);

CREATE TABLE data_sources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config JSON DEFAULT NULL,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT 'incremental',
    status VARCHAR(32) DEFAULT 'active',
    conflict_strategy VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions TINYINT(1) DEFAULT 1,
    last_sync_at TIMESTAMP NULL DEFAULT NULL,
    last_sync_cursor TEXT,
    last_sync_result JSON DEFAULT NULL,
    error_message TEXT,
    sync_log_retention_days INT DEFAULT 30,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_data_sources_tenant_id ON data_sources (tenant_id);
CREATE INDEX idx_data_sources_knowledge_base_id ON data_sources (knowledge_base_id);
CREATE INDEX idx_data_sources_type ON data_sources (type);
CREATE INDEX idx_data_sources_status ON data_sources (status);
CREATE INDEX idx_data_sources_deleted_at ON data_sources (deleted_at);

CREATE TABLE sync_logs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP NULL DEFAULT NULL,
    items_total INT DEFAULT 0,
    items_created INT DEFAULT 0,
    items_updated INT DEFAULT 0,
    items_deleted INT DEFAULT 0,
    items_skipped INT DEFAULT 0,
    items_failed INT DEFAULT 0,
    error_message TEXT,
    result JSON DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_sync_logs_ds FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_sync_logs_data_source_id ON sync_logs (data_source_id);
CREATE INDEX idx_sync_logs_tenant_id ON sync_logs (tenant_id);
CREATE INDEX idx_sync_logs_status ON sync_logs (status);
CREATE INDEX idx_sync_logs_started_at ON sync_logs (started_at);

CREATE TABLE web_search_providers (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSON DEFAULT NULL,
    is_default TINYINT(1) DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_web_search_providers_tenant_id ON web_search_providers (tenant_id);
CREATE INDEX idx_web_search_providers_provider ON web_search_providers (provider);
CREATE INDEX idx_web_search_providers_deleted_at ON web_search_providers (deleted_at);

CREATE TABLE vector_stores (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config JSON NOT NULL DEFAULT ('{}'),
    index_config JSON NOT NULL DEFAULT ('{}'),
    tenant_id INT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_vector_stores_name_tenant ON vector_stores(name, tenant_id);
CREATE INDEX idx_vector_stores_tenant_id ON vector_stores(tenant_id);
CREATE INDEX idx_vector_stores_engine_type ON vector_stores(engine_type);
CREATE INDEX idx_vector_stores_deleted_at ON vector_stores(deleted_at);

CREATE TABLE storage_backends (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSON NOT NULL DEFAULT ('{}'),
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_storage_backends_name_tenant ON storage_backends(tenant_id, name);
CREATE INDEX idx_storage_backends_legacy_alias ON storage_backends(tenant_id, provider);
CREATE INDEX idx_storage_backends_tenant ON storage_backends(tenant_id);

CREATE TABLE resources (
    id VARCHAR(36) PRIMARY KEY,
    handle VARCHAR(22) NOT NULL,
    tenant_id INT NOT NULL,
    storage_backend_id VARCHAR(36),
    provider VARCHAR(32) NOT NULL,
    physical_path TEXT NOT NULL,
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'file',
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    original_name VARCHAR(1024) NOT NULL DEFAULT '',
    size INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    lifecycle VARCHAR(16) NOT NULL DEFAULT 'persistent',
    expires_at TIMESTAMP NULL DEFAULT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_resources_handle ON resources(handle);
CREATE UNIQUE INDEX idx_resources_tenant_location ON resources(tenant_id, location_hash);
CREATE INDEX idx_resources_tenant ON resources(tenant_id);
CREATE INDEX idx_resources_backend ON resources(storage_backend_id);

CREATE TABLE resource_bindings (
    id VARCHAR(36) PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    tenant_id INT NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_resource_bindings_resource FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_resource_bindings_unique ON resource_bindings(resource_id, owner_type, owner_id, relation);
CREATE INDEX idx_resource_bindings_owner ON resource_bindings(tenant_id, owner_type, owner_id);

CREATE TABLE resource_access_grants (
    id VARCHAR(36) PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL,
    resource_id VARCHAR(36) NOT NULL,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_resource_access_grants_resource FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_resource_access_grants_token_hash ON resource_access_grants(token_hash);
CREATE INDEX idx_resource_access_grants_resource ON resource_access_grants(resource_id);
CREATE INDEX idx_resource_access_grants_expires ON resource_access_grants(expires_at);

CREATE TABLE tenant_api_keys (
    id INT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NULL,
    scope_type TEXT NOT NULL DEFAULT ('tenant'),
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    api_key TEXT NOT NULL DEFAULT (''),
    full_access TINYINT(1) NOT NULL DEFAULT 0,
    knowledge_base_ids JSON NOT NULL DEFAULT ('[]'),
    capabilities JSON NOT NULL DEFAULT ('[]'),
    last_used_at TIMESTAMP NULL DEFAULT NULL,
    expires_at TIMESTAMP NULL DEFAULT NULL,
    revoked_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_tenant_api_keys_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_tenant_api_keys_key_hash ON tenant_api_keys(key_hash(255));
CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);
CREATE INDEX idx_tenant_api_keys_scope_type ON tenant_api_keys(scope_type(64));

CREATE TABLE temporary_documents (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref JSON NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size INT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content TEXT NOT NULL DEFAULT (''),
    chunks JSON NOT NULL DEFAULT ('[]'),
    image_refs JSON NOT NULL DEFAULT ('[]'),
    metadata JSON NOT NULL DEFAULT ('{}'),
    processing_options JSON NOT NULL DEFAULT ('{}'),
    token_count INT NOT NULL DEFAULT 0,
    chunk_count INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT (''),
    expires_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP NULL DEFAULT NULL,
    ready_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_temporary_documents_scope ON temporary_documents(tenant_id, session_id);
CREATE INDEX idx_temporary_documents_status ON temporary_documents(status);
CREATE INDEX idx_temporary_documents_expires ON temporary_documents(expires_at);

CREATE TABLE wiki_pages (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36),
    title VARCHAR(512) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    summary TEXT NOT NULL DEFAULT (''),
    content TEXT NOT NULL DEFAULT (''),
    category_path JSON NOT NULL DEFAULT ('[]'),
    wiki_path JSON NOT NULL DEFAULT ('[]'),
    parent_slug VARCHAR(512) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    in_links JSON NOT NULL DEFAULT ('[]'),
    out_links JSON NOT NULL DEFAULT ('[]'),
    synthesis_model_id VARCHAR(64) NOT NULL DEFAULT '',
    embedding_model_id VARCHAR(64) NOT NULL DEFAULT '',
    indexing_strategy VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    run_id VARCHAR(36),
    error_message TEXT,
    failed_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_wiki_pages_slug ON wiki_pages(tenant_id, knowledge_base_id, slug);
CREATE INDEX idx_wiki_pages_kb ON wiki_pages(tenant_id, knowledge_base_id);
CREATE INDEX idx_wiki_pages_parent ON wiki_pages(tenant_id, knowledge_base_id, parent_slug);
CREATE INDEX idx_wiki_pages_status ON wiki_pages(status);

CREATE TABLE wiki_log_entries (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    run_id VARCHAR(36) NOT NULL,
    subject_type VARCHAR(32) NOT NULL DEFAULT '',
    subject_id VARCHAR(36) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    details JSON NOT NULL DEFAULT ('{}'),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_wiki_log_entries_run ON wiki_log_entries(run_id);
CREATE INDEX idx_wiki_log_entries_kb ON wiki_log_entries(tenant_id, knowledge_base_id);
CREATE INDEX idx_wiki_log_entries_subject ON wiki_log_entries(subject_type, subject_id);

CREATE TABLE knowledge_processing_spans (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INT NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    step VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at TIMESTAMP NULL DEFAULT NULL,
    finished_at TIMESTAMP NULL DEFAULT NULL,
    duration_ms INT DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT (''),
    metadata JSON NOT NULL DEFAULT ('{}'),
    span_index INT NOT NULL DEFAULT 0,
    retry_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 3,
    next_retry_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_knowledge_processing_spans_uk ON knowledge_processing_spans(knowledge_id, step, span_index);
CREATE INDEX idx_knowledge_processing_spans_status ON knowledge_processing_spans(status, next_retry_at);
CREATE INDEX idx_knowledge_processing_spans_kb ON knowledge_processing_spans(knowledge_base_id);

CREATE TABLE task_pending_ops (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL DEFAULT '',
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload JSON NOT NULL DEFAULT ('{}'),
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_task_pending_ops_tenant ON task_pending_ops(tenant_id);
CREATE INDEX idx_task_pending_ops_dedup ON task_pending_ops(dedup_key);
CREATE INDEX idx_task_pending_ops_scope ON task_pending_ops(task_type, scope, scope_id);
CREATE INDEX idx_task_pending_ops_claimed ON task_pending_ops(claimed_at);

CREATE TABLE system_settings (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    `key` VARCHAR(128) NOT NULL,
    value JSON NOT NULL DEFAULT ('{}'),
    value_type VARCHAR(16) NOT NULL DEFAULT 'string',
    category VARCHAR(32) NOT NULL DEFAULT 'general',
    description TEXT NOT NULL DEFAULT (''),
    is_secret TINYINT(1) NOT NULL DEFAULT 0,
    requires_restart TINYINT(1) NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_system_settings_key ON system_settings(`key`);

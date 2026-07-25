-- SQLite schema for WeKnora Lite (consolidated from all Postgres migrations)

CREATE TABLE IF NOT EXISTS tenants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    retriever_engines TEXT NOT NULL DEFAULT '[]',
    status VARCHAR(50) DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240,
    storage_used BIGINT NOT NULL DEFAULT 0,
    agent_config TEXT DEFAULT NULL,
    context_config TEXT,
    conversation_config TEXT,
    web_search_config TEXT DEFAULT NULL,
    parser_engine_config TEXT DEFAULT NULL,
    storage_engine_config TEXT DEFAULT NULL,
    default_storage_backend_id VARCHAR(36),
    credentials TEXT DEFAULT NULL,
    chat_history_config TEXT,
    retrieval_config TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT 0,
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    managed_by VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_models_type ON models(type);
CREATE INDEX IF NOT EXISTS idx_models_source ON models(source);
CREATE INDEX IF NOT EXISTS idx_models_is_builtin ON models(is_builtin);
CREATE INDEX IF NOT EXISTS idx_models_managed_by ON models(managed_by);

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INTEGER NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config TEXT NOT NULL DEFAULT '{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}',
    image_processing_config TEXT NOT NULL DEFAULT '{"enable_multimodal": false, "model_id": ""}',
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config TEXT NOT NULL DEFAULT '{}',
    storage_provider_config TEXT DEFAULT NULL,
    vlm_config TEXT NOT NULL DEFAULT '{}',
    extract_config TEXT NULL DEFAULT NULL,
    faq_config TEXT,
    question_generation_config TEXT NULL,
    is_temporary BOOLEAN NOT NULL DEFAULT 0,
    is_pinned INTEGER NOT NULL DEFAULT 0,
    pinned_at DATETIME NULL,
    asr_config TEXT,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    creator_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_vector_store
    ON knowledge_bases(tenant_id, vector_store_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_storage_backend
    ON knowledge_bases(tenant_id, storage_backend_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_creator
    ON knowledge_bases(tenant_id, creator_id);

CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
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
    metadata TEXT,
    tag_id VARCHAR(36),
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result TEXT DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    processed_at DATETIME,
    error_message TEXT,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX IF NOT EXISTS idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX IF NOT EXISTS idx_knowledges_enable_status ON knowledges(enable_status);
CREATE INDEX IF NOT EXISTS idx_knowledges_tag ON knowledges(tag_id);
CREATE INDEX IF NOT EXISTS idx_knowledges_summary_status ON knowledges(summary_status);

CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    title VARCHAR(255),
    description TEXT,
    knowledge_base_id VARCHAR(36),
    max_rounds INTEGER NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT 1,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response TEXT NOT NULL DEFAULT '很抱歉，我暂时无法回答这个问题。',
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters TEXT NOT NULL DEFAULT '{}',
    agent_config TEXT DEFAULT NULL,
    context_config TEXT DEFAULT NULL,
    agent_id VARCHAR(36),
    user_id VARCHAR(512),
    is_pinned BOOLEAN NOT NULL DEFAULT 0,
    pinned_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sessions_agent_id ON sessions(agent_id);
CREATE INDEX IF NOT EXISTS idx_sessions_tenant_user_pin
    ON sessions (tenant_id, user_id, is_pinned, pinned_at, updated_at)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    rendered_content TEXT NOT NULL DEFAULT '',
    knowledge_references TEXT NOT NULL DEFAULT '[]',
    agent_steps TEXT DEFAULT NULL,
    mentioned_items TEXT DEFAULT '[]',
    images TEXT DEFAULT '[]',
    is_completed BOOLEAN NOT NULL DEFAULT 0,
    is_fallback BOOLEAN NOT NULL DEFAULT 0,
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INTEGER NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    execution_context TEXT NOT NULL DEFAULT '{}',
    agent_duration_ms INTEGER DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_messages_knowledge_id ON messages(knowledge_id);
CREATE INDEX IF NOT EXISTS idx_messages_agent_id ON messages(agent_id);

CREATE TABLE IF NOT EXISTS message_suggestion_sets (
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
    allow_regenerate BOOLEAN NOT NULL DEFAULT 0,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions TEXT NOT NULL DEFAULT '[]',
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until DATETIME,
    generated_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_suggestion_sets_cache_key
    ON message_suggestion_sets(tenant_id, assistant_message_id, placement, config_hash, locale);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_sets_session
    ON message_suggestion_sets(tenant_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_sets_status
    ON message_suggestion_sets(status, lease_until);

CREATE TABLE IF NOT EXISTS message_suggestion_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_events_set
    ON message_suggestion_events(suggestion_set_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_events_session
    ON message_suggestion_events(tenant_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_events_type
    ON message_suggestion_events(event_type, created_at);

CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT 1,
    start_at INTEGER NOT NULL,
    end_at INTEGER NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info TEXT,
    video_info TEXT,
    relation_chunks TEXT,
    indirect_relation_chunks TEXT,
    metadata TEXT,
    tag_id VARCHAR(36),
    status INTEGER NOT NULL DEFAULT 0,
    content_hash VARCHAR(64),
    flags INTEGER NOT NULL DEFAULT 1,
    seq_id INTEGER,
    positive_feedback_count INTEGER NOT NULL DEFAULT 0,
    negative_feedback_count INTEGER NOT NULL DEFAULT 0,
    positive_feedback_rate REAL NOT NULL DEFAULT 0,
    recall_weight REAL NOT NULL DEFAULT 1,
    feedback_status VARCHAR(32) NOT NULL DEFAULT 'normal',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
CREATE INDEX IF NOT EXISTS idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunks_chunk_type ON chunks(chunk_type);
CREATE INDEX IF NOT EXISTS idx_chunks_tag ON chunks(tag_id);
CREATE INDEX IF NOT EXISTS idx_chunks_content_hash ON chunks(content_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chunks_seq_id ON chunks(seq_id);
CREATE INDEX IF NOT EXISTS idx_chunks_kb_tenant ON chunks(knowledge_base_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_chunks_knowledge_enabled ON chunks(knowledge_id, is_enabled, deleted_at);
CREATE INDEX IF NOT EXISTS idx_chunks_feedback_status ON chunks(tenant_id, knowledge_base_id, feedback_status);
CREATE INDEX IF NOT EXISTS idx_chunks_feedback_rate ON chunks(tenant_id, knowledge_base_id, positive_feedback_rate);

CREATE TABLE IF NOT EXISTS message_chunk_references (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    request_id VARCHAR(36) NOT NULL DEFAULT '',
    chunk_tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (message_id, chunk_id)
);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_message ON message_chunk_references(tenant_id, message_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_chunk ON message_chunk_references(chunk_tenant_id, chunk_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_kb ON message_chunk_references(chunk_tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_session ON message_chunk_references(tenant_id, session_id);

CREATE TABLE IF NOT EXISTS message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    actor_id VARCHAR(512) NOT NULL,
    rating INTEGER NOT NULL CHECK (rating IN (-1, 1)),
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    reason_detail TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, message_id, actor_id)
);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_session ON message_feedbacks(tenant_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_feedbacks_message ON message_feedbacks(tenant_id, message_id);

CREATE TABLE IF NOT EXISTS message_feedback_attributions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feedback_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    rating INTEGER NOT NULL CHECK (rating IN (-1, 1)),
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    reason_detail TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (feedback_id, chunk_id),
    FOREIGN KEY (feedback_id) REFERENCES message_feedbacks(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_chunk ON message_feedback_attributions(chunk_tenant_id, chunk_id, rating);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_kb ON message_feedback_attributions(chunk_tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_message ON message_feedback_attributions(tenant_id, message_id);
CREATE INDEX IF NOT EXISTS idx_feedback_attributions_reason ON message_feedback_attributions(chunk_tenant_id, reason_code);

CREATE TABLE IF NOT EXISTS chunk_feedback_weight_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    feedback_id VARCHAR(36) NOT NULL DEFAULT '',
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    trigger_source VARCHAR(32) NOT NULL,
    trigger_action VARCHAR(32) NOT NULL,
    old_positive_count INTEGER NOT NULL,
    new_positive_count INTEGER NOT NULL,
    old_negative_count INTEGER NOT NULL,
    new_negative_count INTEGER NOT NULL,
    old_positive_rate REAL NOT NULL,
    new_positive_rate REAL NOT NULL,
    old_recall_weight REAL NOT NULL,
    new_recall_weight REAL NOT NULL,
    old_status VARCHAR(32) NOT NULL,
    new_status VARCHAR(32) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunk_feedback_weight_logs_chunk
    ON chunk_feedback_weight_logs(tenant_id, knowledge_base_id, chunk_id, created_at);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id INTEGER,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT 0,
    -- Per-user JSON preferences (memory toggle, future UI knobs).
    -- SQLite has no JSONB; store as TEXT and let GORM (de)serialise via
    -- the driver.Valuer / sql.Scanner methods on types.UserPreferences.
    preferences TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users(tenant_id);
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);

CREATE TABLE IF NOT EXISTS auth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    token TEXT NOT NULL,
    token_type VARCHAR(50) NOT NULL,
    expires_at DATETIME NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_token ON auth_tokens(token);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_token_type ON auth_tokens(token_type);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_expires_at ON auth_tokens(expires_at);

-- tenant_members carries the per-(user, tenant) TenantRole used by the
-- tenant-level RBAC introduced in #1303. SQLite does not support partial
-- indexes the same way Postgres does, so we use a plain unique index on
-- (user_id, tenant_id) — soft-deleted rows are filtered by the GORM scope.
CREATE TABLE IF NOT EXISTS tenant_members (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36),
    joined_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant_role
    ON tenant_members(tenant_id, role);
CREATE INDEX IF NOT EXISTS idx_tenant_members_user
    ON tenant_members(user_id);

-- audit_logs is the generic per-tenant durability for RBAC events
-- (and future KB / agent / datasource events). Sqlite mirror of the
-- 000044_audit_log migration; same column shape with INTEGER for the
-- BIGSERIAL id and TEXT in place of JSONB for details.
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    actor_user_id VARCHAR(36) NOT NULL DEFAULT '',
    actor_role VARCHAR(32) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL DEFAULT '',
    target_id VARCHAR(64) NOT NULL DEFAULT '',
    target_user_id VARCHAR(36) NOT NULL DEFAULT '',
    request_path VARCHAR(512) NOT NULL DEFAULT '',
    request_method VARCHAR(16) NOT NULL DEFAULT '',
    outcome VARCHAR(16) NOT NULL DEFAULT 'success',
    details TEXT NOT NULL DEFAULT '{}',
    scope_type VARCHAR(32) NOT NULL DEFAULT '',
    scope_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_id_desc
    ON audit_logs(tenant_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor
    ON audit_logs(actor_user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_action
    ON audit_logs(tenant_id, action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at
    ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_scope_desc
    ON audit_logs(tenant_id, scope_type, scope_id, id DESC);

-- user_resource_favorites — sqlite mirror of migration 000047. Same
-- composite PK (user_id, tenant_id, resource_type, resource_id) so the
-- GORM model and FirstOrCreate idempotency carry over.
CREATE TABLE IF NOT EXISTS user_resource_favorites (
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id)
);
CREATE INDEX IF NOT EXISTS idx_user_resource_favorites_user_tenant_type_created_at
    ON user_resource_favorites(user_id, tenant_id, resource_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_resource_favorites_tenant_id
    ON user_resource_favorites(tenant_id);

-- user_kb_pins — sqlite mirror of migration 000050. Per-(user, tenant)
-- pinned knowledge bases; replaces the tenant-wide knowledge_bases.is_pinned
-- column for ordering purposes. The legacy column on knowledge_bases is
-- still defined above for back-compat with existing rows but is no longer
-- written by the application.
CREATE TABLE IF NOT EXISTS user_kb_pins (
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, kb_id)
);
CREATE INDEX IF NOT EXISTS idx_user_kb_pins_user_tenant_pinned_at
    ON user_kb_pins(tenant_id, user_id, pinned_at DESC);

-- tenant_invitations — sqlite mirror of migration 000048. SQLite supports
-- partial unique indexes too, so the same "one pending per (tenant,
-- invitee)" guard can be applied verbatim.
CREATE TABLE IF NOT EXISTS tenant_invitations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL,
    invited_by VARCHAR(36),
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    message VARCHAR(500),
    expires_at DATETIME NOT NULL,
    responded_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tenant_invitations_tenant
    ON tenant_invitations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_invitations_invitee
    ON tenant_invitations(invitee_user_id);

CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order INTEGER NOT NULL DEFAULT 0,
    seq_id INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_tags_kb_name ON knowledge_tags(tenant_id, knowledge_base_id, name);
CREATE INDEX IF NOT EXISTS idx_knowledge_tags_kb ON knowledge_tags(tenant_id, knowledge_base_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_tags_seq_id ON knowledge_tags(seq_id);

CREATE TABLE IF NOT EXISTS mcp_services (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled BOOLEAN DEFAULT 1,
    transport_type VARCHAR(50) NOT NULL,
    url VARCHAR(512),
    headers TEXT,
    auth_config TEXT,
    advanced_config TEXT,
    stdio_config TEXT,
    env_vars TEXT,
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_mcp_services_tenant_id ON mcp_services(tenant_id);
CREATE INDEX IF NOT EXISTS idx_mcp_services_enabled ON mcp_services(enabled);
CREATE INDEX IF NOT EXISTS idx_mcp_services_is_builtin ON mcp_services(is_builtin);
CREATE INDEX IF NOT EXISTS idx_mcp_services_deleted_at ON mcp_services(deleted_at);

CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_tool_approvals_tenant_svc_tool ON mcp_tool_approvals(tenant_id, service_id, tool_name);
CREATE INDEX IF NOT EXISTS idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    client_id VARCHAR(512) NOT NULL,
    client_secret TEXT,
    redirect_uri VARCHAR(1024),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_clients_tenant_svc ON mcp_oauth_clients(tenant_id, service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32),
    expires_at DATETIME,
    refresh_lease_id VARCHAR(36),
    refresh_lease_until DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens(tenant_id, user_id, service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);

CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(36),
    runnable_by_viewer BOOLEAN NOT NULL DEFAULT 1,
    config TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    PRIMARY KEY (id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX IF NOT EXISTS idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX IF NOT EXISTS idx_custom_agents_deleted_at ON custom_agents(deleted_at);

CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    -- Plan 3 (#1303): owning tenant pinned at create time; see migration 000046.
    owner_tenant_id INTEGER NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval BOOLEAN DEFAULT 0,
    invite_code_expires_at DATETIME,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT 0,
    member_limit INTEGER NOT NULL DEFAULT 50,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX IF NOT EXISTS idx_organizations_owner_tenant ON organizations(owner_tenant_id);
CREATE INDEX IF NOT EXISTS idx_organizations_deleted_at ON organizations(deleted_at);

CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_tenant_members_unique ON organization_tenant_members(organization_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_org_tenant_members_by_tenant ON organization_tenant_members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_org_tenant_members_role ON organization_tenant_members(organization_id, role);

CREATE TABLE IF NOT EXISTS kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_kb_shares_org_id ON kb_shares(organization_id);
CREATE INDEX IF NOT EXISTS idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);
CREATE INDEX IF NOT EXISTS idx_kb_shares_deleted_at ON kb_shares(deleted_at);

CREATE TABLE IF NOT EXISTS organization_join_requests (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32),
    message TEXT,
    reviewed_by VARCHAR(36),
    reviewed_at DATETIME,
    review_message TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_org_join_requests_org_id ON organization_join_requests(organization_id);
CREATE INDEX IF NOT EXISTS idx_org_join_requests_user_id ON organization_join_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_org_join_requests_status ON organization_join_requests(status);
-- Plan 3 (#1303): at most one pending request per (org, tenant, type).
-- Approved/rejected rows are not constrained so the audit trail stays.
CREATE UNIQUE INDEX IF NOT EXISTS uq_org_join_requests_pending_per_tenant
    ON organization_join_requests(organization_id, tenant_id, request_type)
    WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    FOREIGN KEY (agent_id, source_tenant_id) REFERENCES custom_agents(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_shares_agent_id ON agent_shares(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_shares_org_id ON agent_shares(organization_id);
CREATE INDEX IF NOT EXISTS idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);
CREATE INDEX IF NOT EXISTS idx_agent_shares_deleted_at ON agent_shares(deleted_at);

CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_tenant_disabled_shared_agents_tenant_id ON tenant_disabled_shared_agents(tenant_id);

CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id VARCHAR(36) PRIMARY KEY,
    platform VARCHAR(20) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    chat_id VARCHAR(128) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) DEFAULT '',
    im_channel_id VARCHAR(36) DEFAULT '',
    thread_id VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id)
    WHERE deleted_at IS NULL AND thread_id != '';
CREATE INDEX IF NOT EXISTS idx_im_channel_tenant ON im_channel_sessions (tenant_id);
CREATE INDEX IF NOT EXISTS idx_im_channel_session ON im_channel_sessions (session_id);
CREATE INDEX IF NOT EXISTS idx_im_channel_sessions_channel ON im_channel_sessions (im_channel_id)
    WHERE im_channel_id != '';

CREATE TABLE IF NOT EXISTS im_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    credentials TEXT NOT NULL DEFAULT '{}',
    knowledge_base_id VARCHAR(36) DEFAULT '',
    bot_identity VARCHAR(255) NOT NULL DEFAULT '',
    session_mode VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_im_channels_tenant ON im_channels (tenant_id);
CREATE INDEX IF NOT EXISTS idx_im_channels_agent ON im_channels (agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_im_channels_bot_identity
    ON im_channels (bot_identity)
    WHERE deleted_at IS NULL AND bot_identity != '';

CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins TEXT NOT NULL DEFAULT '[]',
    welcome_message TEXT NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    rate_limit_per_day INTEGER NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions INTEGER NOT NULL DEFAULT 1,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search INTEGER NOT NULL DEFAULT 0,
    allow_file_upload INTEGER NOT NULL DEFAULT 0,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_embed_channels_tenant ON embed_channels (tenant_id);
CREATE INDEX IF NOT EXISTS idx_embed_channels_agent ON embed_channels (agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_embed_channels_publish_token
    ON embed_channels (publish_token)
    WHERE publish_token != '' AND deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config TEXT,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT 'incremental',
    status VARCHAR(32) DEFAULT 'active',
    conflict_strategy VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions INTEGER DEFAULT 1,
    last_sync_at DATETIME NULL,
    last_sync_cursor TEXT,
    last_sync_result TEXT,
    error_message TEXT,
    sync_log_retention_days INTEGER DEFAULT 30,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
);

CREATE INDEX IF NOT EXISTS idx_data_sources_tenant_id ON data_sources (tenant_id);
CREATE INDEX IF NOT EXISTS idx_data_sources_knowledge_base_id ON data_sources (knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_data_sources_type ON data_sources (type);
CREATE INDEX IF NOT EXISTS idx_data_sources_status ON data_sources (status);
CREATE INDEX IF NOT EXISTS idx_data_sources_deleted_at ON data_sources (deleted_at);

CREATE TABLE IF NOT EXISTS sync_logs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL REFERENCES data_sources(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME NULL,
    items_total INTEGER DEFAULT 0,
    items_created INTEGER DEFAULT 0,
    items_updated INTEGER DEFAULT 0,
    items_deleted INTEGER DEFAULT 0,
    items_skipped INTEGER DEFAULT 0,
    items_failed INTEGER DEFAULT 0,
    error_message TEXT,
    result TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sync_logs_data_source_id ON sync_logs (data_source_id);
CREATE INDEX IF NOT EXISTS idx_sync_logs_tenant_id ON sync_logs (tenant_id);
CREATE INDEX IF NOT EXISTS idx_sync_logs_status ON sync_logs (status);
CREATE INDEX IF NOT EXISTS idx_sync_logs_started_at ON sync_logs (started_at);

CREATE TABLE IF NOT EXISTS web_search_providers (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description TEXT,
    parameters TEXT,
    is_default INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
);

CREATE INDEX IF NOT EXISTS idx_web_search_providers_tenant_id ON web_search_providers (tenant_id);
CREATE INDEX IF NOT EXISTS idx_web_search_providers_provider ON web_search_providers (provider);
CREATE INDEX IF NOT EXISTS idx_web_search_providers_deleted_at ON web_search_providers (deleted_at);

CREATE TABLE IF NOT EXISTS vector_stores (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    engine_type VARCHAR(50) NOT NULL,
    connection_config TEXT NOT NULL DEFAULT '{}',
    index_config TEXT NOT NULL DEFAULT '{}',
    tenant_id INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vector_stores_name_tenant
    ON vector_stores(name, tenant_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_vector_stores_tenant_id ON vector_stores(tenant_id);
CREATE INDEX IF NOT EXISTS idx_vector_stores_engine_type ON vector_stores(engine_type);
CREATE INDEX IF NOT EXISTS idx_vector_stores_deleted_at ON vector_stores(deleted_at);

CREATE TABLE IF NOT EXISTS storage_backends (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config TEXT NOT NULL DEFAULT '{}',
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_backends_name_tenant
    ON storage_backends(tenant_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_backends_legacy_alias
    ON storage_backends(tenant_id, provider) WHERE deleted_at IS NULL AND legacy_alias = 1;
CREATE INDEX IF NOT EXISTS idx_storage_backends_tenant ON storage_backends(tenant_id);

CREATE TABLE IF NOT EXISTS resources (
    id VARCHAR(36) PRIMARY KEY,
    handle VARCHAR(22) NOT NULL UNIQUE,
    tenant_id INTEGER NOT NULL,
    storage_backend_id VARCHAR(36),
    provider VARCHAR(32) NOT NULL,
    physical_path TEXT NOT NULL,
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'file',
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    original_name VARCHAR(1024) NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    lifecycle VARCHAR(16) NOT NULL DEFAULT 'persistent',
    expires_at DATETIME,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_resources_tenant_location
    ON resources(tenant_id, location_hash) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_resources_tenant ON resources(tenant_id);
CREATE INDEX IF NOT EXISTS idx_resources_backend ON resources(storage_backend_id);

CREATE TABLE IF NOT EXISTS resource_bindings (
    id VARCHAR(36) PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_bindings_unique
    ON resource_bindings(resource_id, owner_type, owner_id, relation);
CREATE INDEX IF NOT EXISTS idx_resource_bindings_owner
    ON resource_bindings(tenant_id, owner_type, owner_id);

CREATE TABLE IF NOT EXISTS resource_access_grants (
    id VARCHAR(36) PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    resource_id VARCHAR(36) NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_resource_access_grants_resource
    ON resource_access_grants(resource_id);
CREATE INDEX IF NOT EXISTS idx_resource_access_grants_expires
    ON resource_access_grants(expires_at);

CREATE TABLE IF NOT EXISTS tenant_api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER,
    scope_type TEXT NOT NULL DEFAULT 'tenant'
        CHECK (scope_type IN ('tenant', 'platform')),
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    api_key TEXT NOT NULL DEFAULT '',
    full_access BOOLEAN NOT NULL DEFAULT 0,
    knowledge_base_ids TEXT NOT NULL DEFAULT '[]',
    capabilities TEXT NOT NULL DEFAULT '[]',
    last_used_at DATETIME,
    expires_at DATETIME,
    revoked_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = 0)
    )
);

CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);
CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_scope_type ON tenant_api_keys(scope_type);

CREATE TABLE IF NOT EXISTS temporary_documents (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref TEXT NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content TEXT NOT NULL DEFAULT '',
    chunks TEXT NOT NULL DEFAULT '[]',
    image_refs TEXT NOT NULL DEFAULT '[]',
    metadata TEXT NOT NULL DEFAULT '{}',
    processing_options TEXT NOT NULL DEFAULT '{}',
    token_count INTEGER NOT NULL DEFAULT 0,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    expires_at DATETIME NOT NULL,
    started_at DATETIME,
    ready_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_temporary_documents_scope ON temporary_documents(tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_temporary_documents_status ON temporary_documents(status);
CREATE INDEX IF NOT EXISTS idx_temporary_documents_expires ON temporary_documents(expires_at);

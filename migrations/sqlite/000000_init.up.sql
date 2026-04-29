-- SQLite schema for WeKnora Lite (consolidated from all Postgres migrations)

CREATE TABLE IF NOT EXISTS tenants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    api_key VARCHAR(256) NOT NULL,
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
    credentials TEXT DEFAULT NULL,
    chat_history_config TEXT,
    retrieval_config TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_tenants_api_key ON tenants(api_key);
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT 0,
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_models_type ON models(type);
CREATE INDEX IF NOT EXISTS idx_models_source ON models(source);
CREATE INDEX IF NOT EXISTS idx_models_is_builtin ON models(is_builtin);

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
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_vector_store
    ON knowledge_bases(tenant_id, vector_store_id);

CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    source VARCHAR(128) NOT NULL,
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
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sessions_agent_id ON sessions(agent_id);

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
    agent_duration_ms INTEGER DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_messages_knowledge_id ON messages(knowledge_id);

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

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id INTEGER,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(36),
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
CREATE INDEX IF NOT EXISTS idx_organizations_deleted_at ON organizations(deleted_at);

CREATE TABLE IF NOT EXISTS organization_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_members_org_user ON organization_members(organization_id, user_id);
CREATE INDEX IF NOT EXISTS idx_org_members_user_id ON organization_members(user_id);
CREATE INDEX IF NOT EXISTS idx_org_members_tenant_id ON organization_members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_org_members_role ON organization_members(role);

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

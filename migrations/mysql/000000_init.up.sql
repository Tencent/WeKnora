-- MySQL schema for WeKnora (consolidated from all Postgres migrations)
-- Requires MySQL 8.0.13+ (expression defaults for JSON columns).
-- Use with DB_DRIVER=mysql and an external vector store (Milvus/Qdrant/etc.).

CREATE TABLE IF NOT EXISTS tenants (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    api_key         VARCHAR(256) NOT NULL,
    retriever_engines TEXT NOT NULL DEFAULT ('[]'),
    status          VARCHAR(50) DEFAULT 'active',
    business        VARCHAR(255) NOT NULL,
    storage_quota   BIGINT NOT NULL DEFAULT 10737418240,
    storage_used    BIGINT NOT NULL DEFAULT 0,
    agent_config    TEXT DEFAULT NULL,
    context_config  TEXT,
    conversation_config TEXT,
    web_search_config   TEXT DEFAULT NULL,
    parser_engine_config    TEXT DEFAULT NULL,
    storage_engine_config   TEXT DEFAULT NULL,
    credentials     TEXT DEFAULT NULL,
    chat_history_config TEXT,
    retrieval_config TEXT,
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at      DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_tenants_api_key ON tenants(api_key);
CREATE INDEX idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS models (
    id              VARCHAR(64) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    name            VARCHAR(255) NOT NULL,
    display_name    VARCHAR(255) NOT NULL DEFAULT '',
    type            VARCHAR(50) NOT NULL,
    source          VARCHAR(50) NOT NULL,
    description     TEXT,
    parameters      TEXT NOT NULL,
    is_default      TINYINT(1) NOT NULL DEFAULT 0,
    is_builtin      TINYINT(1) NOT NULL DEFAULT 0,
    managed_by      VARCHAR(32) NOT NULL DEFAULT '',
    status          VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at      DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_models_type ON models(type);
CREATE INDEX idx_models_source ON models(source);
CREATE INDEX idx_models_is_builtin ON models(is_builtin);
CREATE INDEX idx_models_managed_by ON models(managed_by);

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id                      VARCHAR(36) PRIMARY KEY,
    name                    VARCHAR(255) NOT NULL,
    description             TEXT,
    tenant_id               BIGINT NOT NULL,
    type                    VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config         TEXT NOT NULL DEFAULT ('{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}'),
    image_processing_config TEXT NOT NULL DEFAULT ('{"enable_multimodal": false, "model_id": ""}'),
    embedding_model_id      VARCHAR(64) NOT NULL,
    summary_model_id        VARCHAR(64) NOT NULL,
    cos_config              TEXT NOT NULL DEFAULT ('{}'),
    storage_provider_config TEXT DEFAULT NULL,
    vlm_config              TEXT NOT NULL DEFAULT ('{}'),
    extract_config          TEXT NULL DEFAULT NULL,
    faq_config              TEXT,
    question_generation_config TEXT NULL,
    is_temporary            TINYINT(1) NOT NULL DEFAULT 0,
    is_pinned               INT NOT NULL DEFAULT 0,
    pinned_at               DATETIME(6) NULL,
    asr_config              TEXT,
    vector_store_id         VARCHAR(36),
    creator_id              VARCHAR(36),
    wiki_config             JSON DEFAULT NULL,
    indexing_strategy       JSON DEFAULT NULL,
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);
CREATE INDEX idx_knowledge_bases_tenant_vector_store ON knowledge_bases(tenant_id, vector_store_id);
CREATE INDEX idx_knowledge_bases_tenant_creator ON knowledge_bases(tenant_id, creator_id);

CREATE TABLE IF NOT EXISTS knowledges (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    type                VARCHAR(50) NOT NULL,
    title               VARCHAR(255) NOT NULL,
    description         TEXT,
    source              VARCHAR(2048) NOT NULL,
    parse_status        VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    enable_status       VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id  VARCHAR(64),
    file_name           VARCHAR(255),
    file_type           VARCHAR(50),
    file_size           BIGINT,
    file_path           TEXT,
    file_hash           VARCHAR(64),
    storage_size        BIGINT NOT NULL DEFAULT 0,
    metadata            TEXT,
    summary_status      VARCHAR(32) DEFAULT 'none',
    last_faq_import_result TEXT DEFAULT NULL,
    channel             VARCHAR(50) NOT NULL DEFAULT 'web',
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    processed_at        DATETIME(6),
    error_message       TEXT,
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX idx_knowledges_enable_status ON knowledges(enable_status);
CREATE INDEX idx_knowledges_summary_status ON knowledges(summary_status);

CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id    VARCHAR(36) NOT NULL,
    tag_id          VARCHAR(36) NOT NULL,
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (knowledge_id, tag_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_ktr_knowledge ON knowledge_tag_relations(knowledge_id);
CREATE INDEX idx_ktr_tag ON knowledge_tag_relations(tag_id);

CREATE TABLE IF NOT EXISTS sessions (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    title               VARCHAR(255),
    description         TEXT,
    knowledge_base_id   VARCHAR(36),
    max_rounds          INT NOT NULL DEFAULT 5,
    enable_rewrite      TINYINT(1) NOT NULL DEFAULT 1,
    fallback_strategy   VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response   TEXT NOT NULL,
    keyword_threshold   FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold    FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id     VARCHAR(64),
    embedding_top_k     INT NOT NULL DEFAULT 10,
    rerank_top_k        INT NOT NULL DEFAULT 10,
    rerank_threshold    FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id    VARCHAR(64),
    summary_parameters  TEXT NOT NULL DEFAULT ('{}'),
    agent_config        TEXT DEFAULT NULL,
    context_config      TEXT DEFAULT NULL,
    agent_id            VARCHAR(36),
    user_id             VARCHAR(36),
    is_pinned           TINYINT(1) NOT NULL DEFAULT 0,
    pinned_at           DATETIME(6),
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);
-- Note: Postgres has a partial index (WHERE deleted_at IS NULL); MySQL uses a full index.
CREATE INDEX idx_sessions_tenant_user_pin ON sessions(tenant_id, user_id, is_pinned, pinned_at, updated_at);

CREATE TABLE IF NOT EXISTS messages (
    id                  VARCHAR(36) PRIMARY KEY,
    request_id          VARCHAR(36) NOT NULL,
    session_id          VARCHAR(36) NOT NULL,
    role                VARCHAR(50) NOT NULL,
    content             LONGTEXT NOT NULL,
    rendered_content    LONGTEXT NOT NULL DEFAULT (''),
    knowledge_references TEXT NOT NULL DEFAULT ('[]'),
    agent_steps         TEXT DEFAULT NULL,
    mentioned_items     TEXT DEFAULT ('[]'),
    images              TEXT DEFAULT ('[]'),
    is_completed        TINYINT(1) NOT NULL DEFAULT 0,
    is_fallback         TINYINT(1) NOT NULL DEFAULT 0,
    channel             VARCHAR(50) NOT NULL DEFAULT '',
    agent_duration_ms   INT DEFAULT 0,
    knowledge_id        VARCHAR(36),
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_messages_session_id ON messages(session_id);
CREATE INDEX idx_messages_knowledge_id ON messages(knowledge_id);

CREATE TABLE IF NOT EXISTS chunks (
    id                      VARCHAR(36) PRIMARY KEY,
    tenant_id               BIGINT NOT NULL,
    knowledge_base_id       VARCHAR(36) NOT NULL,
    knowledge_id            VARCHAR(36) NOT NULL,
    content                 LONGTEXT NOT NULL,
    chunk_index             INT NOT NULL,
    is_enabled              TINYINT(1) NOT NULL DEFAULT 1,
    start_at                INT NOT NULL,
    end_at                  INT NOT NULL,
    pre_chunk_id            VARCHAR(36),
    next_chunk_id           VARCHAR(36),
    chunk_type              VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id         VARCHAR(36),
    image_info              TEXT,
    video_info              TEXT,
    relation_chunks         TEXT,
    indirect_relation_chunks TEXT,
    metadata                TEXT,
    tag_id                  VARCHAR(36),
    status                  INT NOT NULL DEFAULT 0,
    content_hash            VARCHAR(64),
    flags                   INT NOT NULL DEFAULT 1,
    seq_id                  INT,
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
CREATE INDEX idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX idx_chunks_chunk_type ON chunks(chunk_type);
CREATE INDEX idx_chunks_tag ON chunks(tag_id);
CREATE INDEX idx_chunks_content_hash ON chunks(content_hash);
CREATE UNIQUE INDEX idx_chunks_seq_id ON chunks(seq_id);
CREATE INDEX idx_chunks_kb_tenant ON chunks(knowledge_base_id, tenant_id);
CREATE INDEX idx_chunks_knowledge_enabled ON chunks(knowledge_id, is_enabled, deleted_at);

CREATE TABLE IF NOT EXISTS users (
    id                      VARCHAR(36) PRIMARY KEY,
    username                VARCHAR(100) NOT NULL UNIQUE,
    email                   VARCHAR(255) NOT NULL UNIQUE,
    password_hash           VARCHAR(255) NOT NULL,
    avatar                  VARCHAR(500),
    tenant_id               BIGINT,
    is_active               TINYINT(1) NOT NULL DEFAULT 1,
    can_access_all_tenants  TINYINT(1) NOT NULL DEFAULT 0,
    is_system_admin         TINYINT(1) NOT NULL DEFAULT 0,
    preferences             TEXT NOT NULL DEFAULT ('{}'),
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_deleted_at ON users(deleted_at);
CREATE INDEX idx_users_is_system_admin ON users(is_system_admin);

CREATE TABLE IF NOT EXISTS auth_tokens (
    id          VARCHAR(36) PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    token       TEXT NOT NULL,
    token_type  VARCHAR(50) NOT NULL,
    expires_at  DATETIME(6) NOT NULL,
    is_revoked  TINYINT(1) NOT NULL DEFAULT 0,
    created_at  DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX idx_auth_tokens_token_type ON auth_tokens(token_type);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

CREATE TABLE IF NOT EXISTS tenant_members (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    tenant_id   BIGINT NOT NULL,
    role        VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status      VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by  VARCHAR(36),
    joined_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at  DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique ON tenant_members(user_id, tenant_id);
CREATE INDEX idx_tenant_members_tenant_role ON tenant_members(tenant_id, role);
CREATE INDEX idx_tenant_members_user ON tenant_members(user_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    actor_user_id   VARCHAR(36) NOT NULL DEFAULT '',
    actor_role      VARCHAR(32) NOT NULL DEFAULT '',
    action          VARCHAR(64) NOT NULL,
    target_type     VARCHAR(32) NOT NULL DEFAULT '',
    target_id       VARCHAR(64) NOT NULL DEFAULT '',
    target_user_id  VARCHAR(36) NOT NULL DEFAULT '',
    request_path    VARCHAR(512) NOT NULL DEFAULT '',
    request_method  VARCHAR(16) NOT NULL DEFAULT '',
    outcome         VARCHAR(16) NOT NULL DEFAULT 'success',
    details         TEXT NOT NULL DEFAULT ('{}'),
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_audit_logs_tenant_id_desc ON audit_logs(tenant_id, id);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_user_id);
CREATE INDEX idx_audit_logs_tenant_action ON audit_logs(tenant_id, action);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);

CREATE TABLE IF NOT EXISTS user_resource_favorites (
    user_id         VARCHAR(36) NOT NULL,
    tenant_id       BIGINT NOT NULL,
    resource_type   VARCHAR(16) NOT NULL,
    resource_id     VARCHAR(64) NOT NULL,
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_user_resource_favorites_user_tenant_type_created_at ON user_resource_favorites(user_id, tenant_id, resource_type, created_at);
CREATE INDEX idx_user_resource_favorites_tenant_id ON user_resource_favorites(tenant_id);

CREATE TABLE IF NOT EXISTS user_kb_pins (
    tenant_id   BIGINT NOT NULL,
    user_id     VARCHAR(36) NOT NULL,
    kb_id       VARCHAR(36) NOT NULL,
    pinned_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, user_id, kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_user_kb_pins_user_tenant_pinned_at ON user_kb_pins(tenant_id, user_id, pinned_at);

CREATE TABLE IF NOT EXISTS tenant_invitations (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL,
    invited_by      VARCHAR(36),
    role            VARCHAR(20) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    message         VARCHAR(500),
    expires_at      DATETIME(6) NOT NULL,
    responded_at    DATETIME(6),
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at      DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Note: Postgres uses a partial UNIQUE index (WHERE status='pending' AND deleted_at IS NULL).
-- MySQL does not support partial indexes; uniqueness is enforced at application level.
CREATE INDEX idx_tenant_invitations_unique_pending ON tenant_invitations(tenant_id, invitee_user_id);
CREATE INDEX idx_tenant_invitations_tenant ON tenant_invitations(tenant_id);
CREATE INDEX idx_tenant_invitations_invitee ON tenant_invitations(invitee_user_id);

CREATE TABLE IF NOT EXISTS knowledge_tags (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    name                VARCHAR(128) NOT NULL,
    color               VARCHAR(32),
    sort_order          INT NOT NULL DEFAULT 0,
    seq_id              INT,
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_knowledge_tags_kb_name ON knowledge_tags(tenant_id, knowledge_base_id, name);
CREATE INDEX idx_knowledge_tags_kb ON knowledge_tags(tenant_id, knowledge_base_id);
CREATE UNIQUE INDEX idx_knowledge_tags_seq_id ON knowledge_tags(seq_id);

CREATE TABLE IF NOT EXISTS mcp_services (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    enabled         TINYINT(1) DEFAULT 1,
    transport_type  VARCHAR(50) NOT NULL,
    url             VARCHAR(512),
    headers         TEXT,
    auth_config     TEXT,
    advanced_config TEXT,
    stdio_config    TEXT,
    env_vars        TEXT,
    is_builtin      TINYINT(1) NOT NULL DEFAULT 0,
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at      DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_mcp_services_tenant_id ON mcp_services(tenant_id);
CREATE INDEX idx_mcp_services_enabled ON mcp_services(enabled);
CREATE INDEX idx_mcp_services_is_builtin ON mcp_services(is_builtin);
CREATE INDEX idx_mcp_services_deleted_at ON mcp_services(deleted_at);

CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    service_id          VARCHAR(36) NOT NULL,
    tool_name           VARCHAR(512) NOT NULL,
    require_approval    TINYINT(1) NOT NULL DEFAULT 0,
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_mcp_tool_approvals_tenant_svc_tool ON mcp_tool_approvals(tenant_id, service_id, tool_name);
CREATE INDEX idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    service_id      VARCHAR(36) NOT NULL,
    client_id       VARCHAR(512) NOT NULL,
    client_secret   TEXT,
    redirect_uri    VARCHAR(1024),
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_mcp_oauth_clients_tenant_svc ON mcp_oauth_clients(tenant_id, service_id);
CREATE INDEX idx_mcp_oauth_clients_service_id ON mcp_oauth_clients(service_id);

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    user_id         VARCHAR(64) NOT NULL,
    service_id      VARCHAR(36) NOT NULL,
    access_token    TEXT,
    refresh_token   TEXT,
    token_type      VARCHAR(32),
    expires_at      DATETIME(6),
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_mcp_oauth_tokens_tenant_user_svc ON mcp_oauth_tokens(tenant_id, user_id, service_id);
CREATE INDEX idx_mcp_oauth_tokens_service_id ON mcp_oauth_tokens(service_id);
CREATE INDEX idx_mcp_oauth_tokens_user_id ON mcp_oauth_tokens(user_id);

CREATE TABLE IF NOT EXISTS custom_agents (
    id              VARCHAR(36) NOT NULL,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    avatar          VARCHAR(64),
    is_builtin      TINYINT(1) NOT NULL DEFAULT 0,
    tenant_id       BIGINT NOT NULL,
    created_by      VARCHAR(36),
    runnable_by_viewer TINYINT(1) NOT NULL DEFAULT 1,
    config          TEXT NOT NULL DEFAULT ('{}'),
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at      DATETIME(6),
    PRIMARY KEY (id, tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX idx_custom_agents_deleted_at ON custom_agents(deleted_at);

CREATE TABLE IF NOT EXISTS organizations (
    id                          VARCHAR(36) PRIMARY KEY,
    name                        VARCHAR(255) NOT NULL,
    description                 TEXT,
    owner_id                    VARCHAR(36) NOT NULL,
    owner_tenant_id             BIGINT NOT NULL DEFAULT 0,
    invite_code                 VARCHAR(32),
    require_approval            TINYINT(1) DEFAULT 0,
    invite_code_expires_at      DATETIME(6),
    invite_code_validity_days   SMALLINT NOT NULL DEFAULT 7,
    avatar                      VARCHAR(512) DEFAULT '',
    searchable                  TINYINT(1) NOT NULL DEFAULT 0,
    member_limit                INT NOT NULL DEFAULT 50,
    created_at                  DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at                  DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at                  DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX idx_organizations_owner_tenant ON organizations(owner_tenant_id);
CREATE INDEX idx_organizations_deleted_at ON organizations(deleted_at);

CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id                      VARCHAR(36) PRIMARY KEY,
    organization_id         VARCHAR(36) NOT NULL,
    tenant_id               BIGINT NOT NULL,
    role                    VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id  VARCHAR(36) NOT NULL DEFAULT '',
    joined_at               DATETIME(6),
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_org_tenant_members_unique ON organization_tenant_members(organization_id, tenant_id);
CREATE INDEX idx_org_tenant_members_by_tenant ON organization_tenant_members(tenant_id);
CREATE INDEX idx_org_tenant_members_role ON organization_tenant_members(organization_id, role);

CREATE TABLE IF NOT EXISTS kb_shares (
    id                  VARCHAR(36) PRIMARY KEY,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    organization_id     VARCHAR(36) NOT NULL,
    shared_by_user_id   VARCHAR(36) NOT NULL,
    source_tenant_id    BIGINT NOT NULL,
    permission          VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6),
    FOREIGN KEY (knowledge_base_id) REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);
CREATE INDEX idx_kb_shares_org_id ON kb_shares(organization_id);
CREATE INDEX idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);
CREATE INDEX idx_kb_shares_deleted_at ON kb_shares(deleted_at);

CREATE TABLE IF NOT EXISTS organization_join_requests (
    id              VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    user_id         VARCHAR(36) NOT NULL,
    tenant_id       BIGINT NOT NULL,
    status          VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role  VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type    VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role       VARCHAR(32),
    message         TEXT,
    reviewed_by     VARCHAR(36),
    reviewed_at     DATETIME(6),
    review_message  TEXT,
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_org_join_requests_org_id ON organization_join_requests(organization_id);
CREATE INDEX idx_org_join_requests_user_id ON organization_join_requests(user_id);
CREATE INDEX idx_org_join_requests_status ON organization_join_requests(status);
-- Note: Postgres has partial UNIQUE (WHERE status='pending'); MySQL enforces at app level.
CREATE INDEX uq_org_join_requests_pending_per_tenant ON organization_join_requests(organization_id, tenant_id, request_type);

CREATE TABLE IF NOT EXISTS agent_shares (
    id                  VARCHAR(36) PRIMARY KEY,
    agent_id            VARCHAR(36) NOT NULL,
    organization_id     VARCHAR(36) NOT NULL,
    shared_by_user_id   VARCHAR(36) NOT NULL,
    source_tenant_id    BIGINT NOT NULL,
    permission          VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6),
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_agent_shares_agent_id ON agent_shares(agent_id);
CREATE INDEX idx_agent_shares_org_id ON agent_shares(organization_id);
CREATE INDEX idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);
CREATE INDEX idx_agent_shares_deleted_at ON agent_shares(deleted_at);

CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id       BIGINT NOT NULL,
    agent_id        VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_tenant_disabled_shared_agents_tenant_id ON tenant_disabled_shared_agents(tenant_id);

CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id              VARCHAR(36) PRIMARY KEY,
    platform        VARCHAR(20) NOT NULL,
    user_id         VARCHAR(128) NOT NULL,
    chat_id         VARCHAR(128) NOT NULL DEFAULT '',
    session_id      VARCHAR(36) NOT NULL,
    tenant_id       BIGINT NOT NULL,
    agent_id        VARCHAR(36) DEFAULT '',
    im_channel_id   VARCHAR(36) DEFAULT '',
    thread_id       VARCHAR(128) NOT NULL DEFAULT '',
    status          VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata        TEXT DEFAULT ('{}'),
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at      DATETIME(6),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Note: Postgres has partial UNIQUE indexes (WHERE deleted_at IS NULL). MySQL uses plain indexes.
CREATE INDEX idx_channel_lookup ON im_channel_sessions(platform, user_id, chat_id, tenant_id);
CREATE INDEX idx_channel_thread_lookup ON im_channel_sessions(platform, chat_id, thread_id, tenant_id);
CREATE INDEX idx_im_channel_tenant ON im_channel_sessions(tenant_id);
CREATE INDEX idx_im_channel_session ON im_channel_sessions(session_id);
CREATE INDEX idx_im_channel_sessions_channel ON im_channel_sessions(im_channel_id);

CREATE TABLE IF NOT EXISTS im_channels (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    agent_id            VARCHAR(36) NOT NULL,
    platform            VARCHAR(20) NOT NULL,
    name                VARCHAR(255) NOT NULL DEFAULT '',
    enabled             INT NOT NULL DEFAULT 1,
    mode                VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode         VARCHAR(20) NOT NULL DEFAULT 'stream',
    credentials         TEXT NOT NULL DEFAULT ('{}'),
    knowledge_base_id   VARCHAR(36) DEFAULT '',
    bot_identity        VARCHAR(255) NOT NULL DEFAULT '',
    session_mode        VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_im_channels_tenant ON im_channels(tenant_id);
CREATE INDEX idx_im_channels_agent ON im_channels(agent_id);
-- Note: Postgres has partial UNIQUE (WHERE deleted_at IS NULL AND bot_identity != '').
CREATE INDEX idx_im_channels_bot_identity ON im_channels(bot_identity);

CREATE TABLE IF NOT EXISTS embed_channels (
    id                      VARCHAR(36) PRIMARY KEY,
    tenant_id               BIGINT NOT NULL,
    agent_id                VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name                    VARCHAR(255) NOT NULL DEFAULT '',
    enabled                 INT NOT NULL DEFAULT 1,
    publish_token           VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins         TEXT NOT NULL DEFAULT ('[]'),
    welcome_message         TEXT NOT NULL DEFAULT (''),
    rate_limit_per_minute   INT NOT NULL DEFAULT 30,
    rate_limit_per_day      INT NOT NULL DEFAULT 10000,
    primary_color           VARCHAR(32) NOT NULL DEFAULT '',
    page_title              VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode       VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions INT NOT NULL DEFAULT 1,
    widget_position         VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search        INT NOT NULL DEFAULT 0,
    allow_memory            INT NOT NULL DEFAULT 0,
    allow_file_upload       INT NOT NULL DEFAULT 0,
    default_locale          VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url             VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret          VARCHAR(128) NOT NULL DEFAULT '',
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_embed_channels_tenant ON embed_channels(tenant_id);
CREATE INDEX idx_embed_channels_agent ON embed_channels(agent_id);
-- Note: Postgres has partial UNIQUE (WHERE publish_token != '' AND deleted_at IS NULL).
CREATE INDEX idx_embed_channels_publish_token ON embed_channels(publish_token);

CREATE TABLE IF NOT EXISTS data_sources (
    id                      VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id               BIGINT NOT NULL,
    knowledge_base_id       VARCHAR(36) NOT NULL,
    name                    VARCHAR(255) NOT NULL,
    type                    VARCHAR(50) NOT NULL,
    config                  TEXT,
    sync_schedule           VARCHAR(100),
    sync_mode               VARCHAR(20) DEFAULT 'incremental',
    status                  VARCHAR(32) DEFAULT 'active',
    conflict_strategy       VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions          INT DEFAULT 1,
    last_sync_at            DATETIME(6) NULL,
    last_sync_cursor        TEXT,
    last_sync_result        TEXT,
    error_message           TEXT,
    sync_log_retention_days INT DEFAULT 30,
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_data_sources_tenant_id ON data_sources(tenant_id);
CREATE INDEX idx_data_sources_knowledge_base_id ON data_sources(knowledge_base_id);
CREATE INDEX idx_data_sources_type ON data_sources(type);
CREATE INDEX idx_data_sources_status ON data_sources(status);
CREATE INDEX idx_data_sources_deleted_at ON data_sources(deleted_at);

CREATE TABLE IF NOT EXISTS sync_logs (
    id              VARCHAR(36) NOT NULL PRIMARY KEY,
    data_source_id  VARCHAR(36) NOT NULL,
    tenant_id       BIGINT NOT NULL,
    status          VARCHAR(32) NOT NULL,
    started_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    finished_at     DATETIME(6) NULL,
    items_total     INT DEFAULT 0,
    items_created   INT DEFAULT 0,
    items_updated   INT DEFAULT 0,
    items_deleted   INT DEFAULT 0,
    items_skipped   INT DEFAULT 0,
    items_failed    INT DEFAULT 0,
    error_message   TEXT,
    result          TEXT,
    created_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_sync_logs_data_source_id ON sync_logs(data_source_id);
CREATE INDEX idx_sync_logs_tenant_id ON sync_logs(tenant_id);
CREATE INDEX idx_sync_logs_status ON sync_logs(status);
CREATE INDEX idx_sync_logs_started_at ON sync_logs(started_at);

CREATE TABLE IF NOT EXISTS web_search_providers (
    id          VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    name        VARCHAR(255) NOT NULL,
    provider    VARCHAR(50) NOT NULL,
    description TEXT,
    parameters  TEXT,
    is_default  INT DEFAULT 0,
    created_at  DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at  DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_web_search_providers_tenant_id ON web_search_providers(tenant_id);
CREATE INDEX idx_web_search_providers_provider ON web_search_providers(provider);
CREATE INDEX idx_web_search_providers_deleted_at ON web_search_providers(deleted_at);

CREATE TABLE IF NOT EXISTS vector_stores (
    id                  VARCHAR(36) NOT NULL PRIMARY KEY,
    name                VARCHAR(255) NOT NULL,
    engine_type         VARCHAR(50) NOT NULL,
    connection_config   TEXT NOT NULL DEFAULT ('{}'),
    index_config        TEXT NOT NULL DEFAULT ('{}'),
    tenant_id           BIGINT NOT NULL,
    created_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Note: Postgres has partial UNIQUE (WHERE deleted_at IS NULL); MySQL uses a plain index.
CREATE INDEX idx_vector_stores_name_tenant ON vector_stores(name, tenant_id);
CREATE INDEX idx_vector_stores_tenant_id ON vector_stores(tenant_id);
CREATE INDEX idx_vector_stores_engine_type ON vector_stores(engine_type);
CREATE INDEX idx_vector_stores_deleted_at ON vector_stores(deleted_at);

CREATE TABLE IF NOT EXISTS wiki_pages (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    slug                VARCHAR(255) NOT NULL,
    title               VARCHAR(512) NOT NULL DEFAULT '',
    page_type           VARCHAR(32) NOT NULL DEFAULT 'summary',
    status              VARCHAR(32) NOT NULL DEFAULT 'published',
    content             LONGTEXT NOT NULL DEFAULT (''),
    summary             TEXT NOT NULL DEFAULT (''),
    parent_slug         VARCHAR(255) NOT NULL DEFAULT '',
    folder_id           VARCHAR(36) NOT NULL DEFAULT '',
    category_path       JSON,
    wiki_path           VARCHAR(1024) NOT NULL DEFAULT '',
    depth               INT NOT NULL DEFAULT 0,
    sort_order          INT NOT NULL DEFAULT 0,
    source_refs         JSON,
    chunk_refs          JSON,
    in_links            JSON,
    out_links           JSON,
    page_metadata       JSON,
    aliases             JSON,
    version             INT NOT NULL DEFAULT 1,
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Note: Postgres has partial UNIQUE (WHERE deleted_at IS NULL).
CREATE UNIQUE INDEX idx_wiki_pages_kb_slug ON wiki_pages(knowledge_base_id, slug);
CREATE INDEX idx_wiki_pages_kb_id ON wiki_pages(knowledge_base_id);
CREATE INDEX idx_wiki_pages_page_type ON wiki_pages(knowledge_base_id, page_type);
CREATE INDEX idx_wiki_pages_parent_slug ON wiki_pages(knowledge_base_id, parent_slug);
CREATE INDEX idx_wiki_pages_tree ON wiki_pages(knowledge_base_id, page_type, wiki_path(100), sort_order, title(100));
CREATE INDEX idx_wiki_pages_folder_id ON wiki_pages(folder_id);

CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id                      VARCHAR(36) PRIMARY KEY,
    tenant_id               BIGINT NOT NULL,
    knowledge_base_id       VARCHAR(36) NOT NULL,
    slug                    VARCHAR(255) NOT NULL,
    issue_type              VARCHAR(50) NOT NULL,
    description             TEXT NOT NULL,
    suspected_knowledge_ids JSON,
    status                  VARCHAR(20) NOT NULL DEFAULT 'pending',
    reported_by             VARCHAR(100) NOT NULL,
    created_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at              DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_wiki_page_issues_tenant_id ON wiki_page_issues(tenant_id);
CREATE INDEX idx_wiki_page_issues_knowledge_base_id ON wiki_page_issues(knowledge_base_id);
CREATE INDEX idx_wiki_page_issues_slug ON wiki_page_issues(slug);
CREATE INDEX idx_wiki_page_issues_status ON wiki_page_issues(status);

CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id                  BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    action              VARCHAR(32) NOT NULL,
    knowledge_id        VARCHAR(36) NOT NULL DEFAULT '',
    doc_title           TEXT NOT NULL DEFAULT (''),
    summary             TEXT NOT NULL DEFAULT (''),
    pages_affected      JSON,
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_wiki_log_entries_kb_id_desc ON wiki_log_entries(knowledge_base_id, id);
CREATE INDEX idx_wiki_log_entries_tenant_id ON wiki_log_entries(tenant_id);

CREATE TABLE IF NOT EXISTS wiki_folders (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    parent_id           VARCHAR(36) NOT NULL DEFAULT '',
    name                VARCHAR(255) NOT NULL,
    path                VARCHAR(1024) NOT NULL DEFAULT '',
    depth               INT NOT NULL DEFAULT 0,
    sort_order          INT NOT NULL DEFAULT 0,
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Note: Postgres has partial UNIQUE (WHERE deleted_at IS NULL).
CREATE UNIQUE INDEX idx_wiki_folders_parent_name ON wiki_folders(knowledge_base_id, parent_id, name);
CREATE INDEX idx_wiki_folders_parent ON wiki_folders(knowledge_base_id, parent_id);
CREATE INDEX idx_wiki_folders_deleted_at ON wiki_folders(deleted_at);

CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    op          VARCHAR(32) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT '',
    payload     JSON NOT NULL,
    fail_count  INT NOT NULL DEFAULT 0,
    enqueued_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    claimed_at  DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_task_pending_ops_scope ON task_pending_ops(task_type, scope, scope_id, id);
CREATE INDEX idx_task_pending_ops_tenant ON task_pending_ops(tenant_id);

CREATE TABLE IF NOT EXISTS task_dead_letters (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    related_id  VARCHAR(64) NOT NULL DEFAULT '',
    payload     JSON NOT NULL,
    last_error  TEXT NOT NULL DEFAULT (''),
    fail_count  INT NOT NULL,
    failed_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_task_dead_letters_scope ON task_dead_letters(scope, scope_id, failed_at);
CREATE INDEX idx_task_dead_letters_tenant ON task_dead_letters(tenant_id, failed_at);
CREATE INDEX idx_task_dead_letters_task_type ON task_dead_letters(task_type, failed_at);

CREATE TABLE IF NOT EXISTS system_settings (
    id                  BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `key`               VARCHAR(128) NOT NULL UNIQUE,
    value               JSON NOT NULL,
    value_type          VARCHAR(16) NOT NULL,
    category            VARCHAR(32) NOT NULL,
    description         TEXT NOT NULL DEFAULT (''),
    is_secret           TINYINT(1) NOT NULL DEFAULT 0,
    requires_restart    TINYINT(1) NOT NULL DEFAULT 0,
    last_modified_by    VARCHAR(36) NOT NULL DEFAULT '',
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_system_settings_category ON system_settings(category);

CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    knowledge_id    VARCHAR(64) NOT NULL,
    attempt         INT NOT NULL DEFAULT 1,
    span_id         VARCHAR(64) NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(64) NOT NULL,
    kind            VARCHAR(16) NOT NULL,
    status          VARCHAR(16) NOT NULL,
    input           JSON,
    output          JSON,
    metadata        JSON,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    started_at      DATETIME(6),
    finished_at     DATETIME(6),
    duration_ms     BIGINT,
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_kpspan_knowledge_attempt ON knowledge_processing_spans(knowledge_id, attempt);
CREATE INDEX idx_kpspan_status_started ON knowledge_processing_spans(status, started_at);
CREATE INDEX idx_kpspan_parent ON knowledge_processing_spans(parent_span_id);

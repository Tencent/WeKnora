-- MySQL 8 translation of 000012_organizations.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    invite_code VARCHAR(32),
    require_approval TINYINT(1) DEFAULT 0,
    invite_code_expires_at TIMESTAMP,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable TINYINT(1) NOT NULL DEFAULT 0,
    member_limit INTEGER NOT NULL DEFAULT 50,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);
CREATE INDEX idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX idx_organizations_deleted_at ON organizations(deleted_at);
CREATE TABLE organization_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_org_members_user_id ON organization_members(user_id);
CREATE INDEX idx_org_members_tenant_id ON organization_members(tenant_id);
CREATE INDEX idx_org_members_role ON organization_members(role);
CREATE TABLE kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);
CREATE INDEX idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);
CREATE INDEX idx_kb_shares_org_id ON kb_shares(organization_id);
CREATE INDEX idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);
CREATE INDEX idx_kb_shares_deleted_at ON kb_shares(deleted_at);
CREATE TABLE organization_join_requests (
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
    reviewed_at TIMESTAMP,
    review_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_org_join_requests_org_id ON organization_join_requests(organization_id);
CREATE INDEX idx_org_join_requests_user_id ON organization_join_requests(user_id);
CREATE INDEX idx_org_join_requests_status ON organization_join_requests(status);
CREATE INDEX idx_org_join_requests_type ON organization_join_requests(request_type);
CREATE TABLE agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE INDEX idx_agent_shares_agent_id ON agent_shares(agent_id);
CREATE INDEX idx_agent_shares_org_id ON agent_shares(organization_id);
CREATE INDEX idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);
CREATE INDEX idx_agent_shares_deleted_at ON agent_shares(deleted_at);
CREATE TABLE tenant_disabled_shared_agents (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
);
CREATE INDEX idx_tenant_disabled_shared_agents_tenant_id ON tenant_disabled_shared_agents(tenant_id);

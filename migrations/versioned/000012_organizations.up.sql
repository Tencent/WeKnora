-- Migration: 000012_organizations (merged 000012, 000013, 000014)
-- Description: Organization tables, kb_shares, join requests, agent_shares, tenant_disabled_shared_agents
DO $$ BEGIN RAISE NOTICE '[Migration 000012] Starting organization tables setup...'; END $$;

-- Create organizations table
CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    invite_code VARCHAR(32),
    require_approval BOOLEAN DEFAULT FALSE,
    invite_code_expires_at TIMESTAMP WITH TIME ZONE,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT FALSE,
    member_limit INTEGER NOT NULL DEFAULT 50,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_organizations_invite_code ON organizations(invite_code) WHERE invite_code IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_organizations_owner_id ON organizations(owner_id);
CREATE INDEX IF NOT EXISTS idx_organizations_deleted_at ON organizations(deleted_at);

COMMENT ON TABLE organizations IS 'Organizations for cross-tenant collaboration';
COMMENT ON COLUMN organizations.owner_id IS 'User ID of the organization owner';
COMMENT ON COLUMN organizations.invite_code IS 'Unique invitation code for joining the organization';
COMMENT ON COLUMN organizations.require_approval IS 'Whether joining this organization requires admin approval';
COMMENT ON COLUMN organizations.invite_code_expires_at IS 'When the current invite code expires; NULL means no expiry (legacy)';
COMMENT ON COLUMN organizations.invite_code_validity_days IS 'Invite link validity in days: 0=never expire, 1/7/30 days';
COMMENT ON COLUMN organizations.searchable IS 'When true, space appears in search and can be joined by org ID';
COMMENT ON COLUMN organizations.member_limit IS 'Max members allowed; 0 means no limit';

-- Create organization_members table
CREATE TABLE IF NOT EXISTS organization_members (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_members_org_user ON organization_members(organization_id, user_id);
CREATE INDEX IF NOT EXISTS idx_org_members_user_id ON organization_members(user_id);
CREATE INDEX IF NOT EXISTS idx_org_members_tenant_id ON organization_members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_org_members_role ON organization_members(role);

COMMENT ON TABLE organization_members IS 'Members of organizations with their roles';
COMMENT ON COLUMN organization_members.role IS 'Member role: admin, editor, or viewer';
COMMENT ON COLUMN organization_members.tenant_id IS 'The tenant ID that the member belongs to';

-- Create kb_shares table (knowledge base sharing)
CREATE TABLE IF NOT EXISTS kb_shares (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_shares_kb_org ON kb_shares(knowledge_base_id, organization_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_kb_shares_kb_id ON kb_shares(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_kb_shares_org_id ON kb_shares(organization_id);
CREATE INDEX IF NOT EXISTS idx_kb_shares_source_tenant ON kb_shares(source_tenant_id);
CREATE INDEX IF NOT EXISTS idx_kb_shares_deleted_at ON kb_shares(deleted_at);

COMMENT ON TABLE kb_shares IS 'Knowledge base sharing records to organizations';
COMMENT ON COLUMN kb_shares.source_tenant_id IS 'Original tenant ID of the knowledge base for cross-tenant embedding model access';
COMMENT ON COLUMN kb_shares.permission IS 'Access permission level: admin, editor, or viewer';

-- Create organization_join_requests table
CREATE TABLE IF NOT EXISTS organization_join_requests (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    requested_role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    request_type VARCHAR(32) NOT NULL DEFAULT 'join',
    prev_role VARCHAR(32),
    message TEXT,
    reviewed_by VARCHAR(36),
    reviewed_at TIMESTAMP WITH TIME ZONE,
    review_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_join_requests_org_user_pending ON organization_join_requests(organization_id, user_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_org_join_requests_org_id ON organization_join_requests(organization_id);
CREATE INDEX IF NOT EXISTS idx_org_join_requests_user_id ON organization_join_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_org_join_requests_status ON organization_join_requests(status);
CREATE INDEX IF NOT EXISTS idx_org_join_requests_type ON organization_join_requests(request_type);

COMMENT ON TABLE organization_join_requests IS 'Join requests for organizations that require approval';
COMMENT ON COLUMN organization_join_requests.status IS 'Request status: pending, approved, rejected';
COMMENT ON COLUMN organization_join_requests.requested_role IS 'Role requested by the applicant: admin, editor, viewer';
COMMENT ON COLUMN organization_join_requests.request_type IS 'join for new member, upgrade for role upgrade';
COMMENT ON COLUMN organization_join_requests.message IS 'Optional message from the requester';
COMMENT ON COLUMN organization_join_requests.reviewed_by IS 'User ID of the admin who reviewed the request';
COMMENT ON COLUMN organization_join_requests.review_message IS 'Optional message from the reviewer';

-- Agent shares (merged from 000013; model_shares omitted, dropped in 000014)
CREATE TABLE IF NOT EXISTS agent_shares (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    FOREIGN KEY (agent_id, source_tenant_id) REFERENCES custom_agents(id, tenant_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_shares_agent_org ON agent_shares(agent_id, source_tenant_id, organization_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_agent_shares_agent_id ON agent_shares(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_shares_org_id ON agent_shares(organization_id);
CREATE INDEX IF NOT EXISTS idx_agent_shares_source_tenant ON agent_shares(source_tenant_id);
CREATE INDEX IF NOT EXISTS idx_agent_shares_deleted_at ON agent_shares(deleted_at);

COMMENT ON TABLE agent_shares IS 'Custom agent sharing records to organizations';
COMMENT ON COLUMN agent_shares.source_tenant_id IS 'Original tenant ID of the agent';
COMMENT ON COLUMN agent_shares.permission IS 'Access permission: viewer or editor';

-- Per-tenant "disabled" list for shared agents (merged from 000014)
DO $$ BEGIN RAISE NOTICE '[Migration 000012] Creating table: tenant_disabled_shared_agents'; END $$;
CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_disabled_shared_agents_tenant_id ON tenant_disabled_shared_agents(tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000012] Organization tables and tenant_disabled_shared_agents setup completed successfully!'; END $$;

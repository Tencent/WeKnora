-- MySQL 8 translation of 000043_tenant_rbac.
CREATE TABLE tenant_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by VARCHAR(36),
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE INDEX idx_tenant_members_tenant_role ON tenant_members(tenant_id, role);
CREATE INDEX idx_tenant_members_user ON tenant_members(user_id);
ALTER TABLE knowledge_bases ADD COLUMN creator_id VARCHAR(36);
CREATE INDEX idx_knowledge_bases_tenant_creator ON knowledge_bases(tenant_id, creator_id);
ALTER TABLE custom_agents ADD COLUMN runnable_by_viewer TINYINT(1) NOT NULL DEFAULT 1;

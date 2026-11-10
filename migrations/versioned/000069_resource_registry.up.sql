CREATE TABLE IF NOT EXISTS resources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    handle VARCHAR(22) NOT NULL UNIQUE,
    tenant_id BIGINT NOT NULL,
    storage_backend_id VARCHAR(36),
    provider VARCHAR(32) NOT NULL,
    physical_path TEXT NOT NULL,
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'file',
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    original_name VARCHAR(1024) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    lifecycle VARCHAR(16) NOT NULL DEFAULT 'persistent',
    expires_at TIMESTAMP NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_resources_tenant_location
    ON resources(tenant_id, location_hash) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_resources_tenant ON resources(tenant_id);
CREATE INDEX IF NOT EXISTS idx_resources_backend ON resources(storage_backend_id);

CREATE TABLE IF NOT EXISTS resource_bindings (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_bindings_unique
    ON resource_bindings(resource_id, owner_type, owner_id, relation);
CREATE INDEX IF NOT EXISTS idx_resource_bindings_owner
    ON resource_bindings(tenant_id, owner_type, owner_id);

CREATE TABLE IF NOT EXISTS resource_access_grants (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    resource_id VARCHAR(36) NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_resource_access_grants_resource
    ON resource_access_grants(resource_id);
CREATE INDEX IF NOT EXISTS idx_resource_access_grants_expires
    ON resource_access_grants(expires_at);

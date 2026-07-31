CREATE TABLE resources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    handle VARCHAR(22) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    tenant_id BIGINT NOT NULL,
    storage_backend_id VARCHAR(36) NULL,
    provider VARCHAR(32) NOT NULL,
    physical_path TEXT NOT NULL,
    location_hash VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'file',
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    original_name VARCHAR(1024) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    lifecycle VARCHAR(16) NOT NULL DEFAULT 'persistent',
    expires_at DATETIME(3) NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL,
    active_unique_key TINYINT
        AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_resources_tenant_location
    ON resources(tenant_id, location_hash, active_unique_key);
CREATE INDEX idx_resources_tenant ON resources(tenant_id);
CREATE INDEX idx_resources_backend ON resources(storage_backend_id);

CREATE TABLE resource_bindings (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    owner_type VARCHAR(32) NOT NULL,
    owner_id VARCHAR(64) NOT NULL,
    relation VARCHAR(32) NOT NULL DEFAULT 'attachment',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_resource_bindings_resource
        FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_resource_bindings_unique
    ON resource_bindings(resource_id, owner_type, owner_id, relation);
CREATE INDEX idx_resource_bindings_owner
    ON resource_bindings(tenant_id, owner_type, owner_id);

CREATE TABLE resource_access_grants (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    resource_id VARCHAR(36) NOT NULL,
    access_scope VARCHAR(16) NOT NULL DEFAULT 'read',
    expires_at DATETIME(3) NOT NULL,
    revoked_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_resource_access_grants_resource
        FOREIGN KEY (resource_id) REFERENCES resources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_resource_access_grants_resource
    ON resource_access_grants(resource_id);
CREATE INDEX idx_resource_access_grants_expires
    ON resource_access_grants(expires_at);

-- MySQL 8 translation of 000065_tenant_api_keys.
CREATE TABLE tenant_api_keys (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(128) NOT NULL,
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    api_key TEXT NOT NULL,
    full_access TINYINT(1) NOT NULL DEFAULT 0,
    knowledge_base_ids JSON NOT NULL,
    capabilities JSON NOT NULL,
    last_used_at TIMESTAMP NULL,
    expires_at TIMESTAMP NULL,
    revoked_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_tenant_api_keys_tenant FOREIGN KEY (tenant_id)
        REFERENCES tenants(id) ON DELETE CASCADE
);

CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);

INSERT IGNORE INTO tenant_api_keys (
    tenant_id, name, key_hash, api_key, full_access, knowledge_base_ids,
    capabilities, created_at, updated_at
)
SELECT id, 'Tenant API key', CONCAT('migrated-tenant-', id), api_key, 1,
       JSON_ARRAY(), JSON_ARRAY(), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM tenants
WHERE COALESCE(api_key, '') <> '';

DROP INDEX idx_tenants_api_key ON tenants;
ALTER TABLE tenants DROP COLUMN api_key;

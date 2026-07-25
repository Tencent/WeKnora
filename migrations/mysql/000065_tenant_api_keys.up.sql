CREATE TABLE IF NOT EXISTS tenant_api_keys (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    api_key TEXT NOT NULL,
    full_access BOOLEAN NOT NULL DEFAULT FALSE,
    knowledge_base_ids JSON NOT NULL DEFAULT (JSON_ARRAY()),
    capabilities JSON NOT NULL DEFAULT (JSON_ARRAY()),
    last_used_at DATETIME(3) NULL,
    expires_at DATETIME(3) NULL,
    revoked_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_tenant_api_keys_tenant
        FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);

INSERT INTO tenant_api_keys (
    tenant_id,
    name,
    key_hash,
    api_key,
    full_access,
    knowledge_base_ids,
    created_at,
    updated_at
)
SELECT
    id,
    'Tenant API key',
    CONCAT('migrated-tenant-', id),
    api_key,
    TRUE,
    JSON_ARRAY(),
    CURRENT_TIMESTAMP(3),
    CURRENT_TIMESTAMP(3)
FROM tenants
WHERE COALESCE(api_key, '') <> ''
ON DUPLICATE KEY UPDATE key_hash = key_hash;

DROP INDEX idx_tenants_api_key ON tenants;
ALTER TABLE tenants DROP COLUMN api_key;

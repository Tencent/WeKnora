DELETE FROM tenant_api_keys WHERE scope_type = 'platform';

DROP INDEX IF EXISTS idx_tenant_api_keys_tenant;
DROP INDEX IF EXISTS idx_tenant_api_keys_revoked_at;
DROP INDEX IF EXISTS idx_tenant_api_keys_scope_type;

ALTER TABLE tenant_api_keys RENAME TO tenant_api_keys_platform_scope;

CREATE TABLE tenant_api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
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
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);

INSERT INTO tenant_api_keys (
    id, tenant_id, name, key_hash, api_key, full_access,
    knowledge_base_ids, capabilities, last_used_at, expires_at, revoked_at,
    created_at, updated_at
)
SELECT
    id, tenant_id, name, key_hash, api_key, full_access,
    knowledge_base_ids, capabilities, last_used_at, expires_at, revoked_at,
    created_at, updated_at
FROM tenant_api_keys_platform_scope;

DROP TABLE tenant_api_keys_platform_scope;

CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);

DROP INDEX IF EXISTS idx_tenant_api_keys_tenant;
DROP INDEX IF EXISTS idx_tenant_api_keys_revoked_at;

ALTER TABLE tenant_api_keys RENAME TO tenant_api_keys_legacy;

CREATE TABLE tenant_api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER,
    scope_type TEXT NOT NULL DEFAULT 'tenant'
        CHECK (scope_type IN ('tenant', 'platform')),
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
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = 0)
    )
);

INSERT INTO tenant_api_keys (
    id, tenant_id, scope_type, name, key_hash, api_key, full_access,
    knowledge_base_ids, capabilities, last_used_at, expires_at, revoked_at,
    created_at, updated_at
)
SELECT
    id, tenant_id, 'tenant', name, key_hash, api_key, full_access,
    knowledge_base_ids, capabilities, last_used_at, expires_at, revoked_at,
    created_at, updated_at
FROM tenant_api_keys_legacy;

DROP TABLE tenant_api_keys_legacy;

CREATE INDEX idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX idx_tenant_api_keys_revoked_at ON tenant_api_keys(revoked_at);
CREATE INDEX idx_tenant_api_keys_scope_type ON tenant_api_keys(scope_type);

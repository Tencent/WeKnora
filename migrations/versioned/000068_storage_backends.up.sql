CREATE TABLE IF NOT EXISTS storage_backends (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_backends_name_tenant ON storage_backends(tenant_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_backends_legacy_alias ON storage_backends(tenant_id, provider) WHERE deleted_at IS NULL AND legacy_alias = TRUE;
CREATE INDEX IF NOT EXISTS idx_storage_backends_tenant ON storage_backends(tenant_id);
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS default_storage_backend_id VARCHAR(36);
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS storage_backend_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_storage_backend ON knowledge_bases(tenant_id, storage_backend_id);

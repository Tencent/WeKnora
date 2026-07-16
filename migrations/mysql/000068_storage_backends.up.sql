CREATE TABLE storage_backends (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSON NOT NULL DEFAULT (JSON_OBJECT()),
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(3) NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL,
    active_name VARCHAR(255) GENERATED ALWAYS AS (
        CASE WHEN deleted_at IS NULL THEN name ELSE NULL END
    ) STORED,
    active_legacy_provider VARCHAR(32) GENERATED ALWAYS AS (
        CASE WHEN deleted_at IS NULL AND legacy_alias = TRUE THEN provider ELSE NULL END
    ) STORED
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_storage_backends_name_tenant
    ON storage_backends(tenant_id, active_name);
CREATE UNIQUE INDEX idx_storage_backends_legacy_alias
    ON storage_backends(tenant_id, active_legacy_provider);
CREATE INDEX idx_storage_backends_tenant ON storage_backends(tenant_id);

ALTER TABLE tenants ADD COLUMN default_storage_backend_id VARCHAR(36) NULL;
ALTER TABLE knowledge_bases ADD COLUMN storage_backend_id VARCHAR(36) NULL;
CREATE INDEX idx_knowledge_bases_storage_backend
    ON knowledge_bases(tenant_id, storage_backend_id);

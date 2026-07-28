-- MySQL 8 translation of 000068_storage_backends.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE storage_backends (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    config JSON NOT NULL,
    source VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    legacy_alias TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE INDEX idx_storage_backends_tenant ON storage_backends(tenant_id);
ALTER TABLE tenants ADD COLUMN default_storage_backend_id VARCHAR(36);
ALTER TABLE knowledge_bases ADD COLUMN storage_backend_id VARCHAR(36);
CREATE INDEX idx_knowledge_bases_storage_backend ON knowledge_bases(tenant_id, storage_backend_id);

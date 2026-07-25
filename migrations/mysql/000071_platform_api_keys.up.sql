-- MySQL 8 translation of 000071_platform_api_keys.
ALTER TABLE tenant_api_keys
    DROP FOREIGN KEY fk_tenant_api_keys_tenant,
    ADD COLUMN scope_type VARCHAR(16) NOT NULL DEFAULT 'tenant',
    MODIFY COLUMN tenant_id INTEGER NULL,
    ADD CONSTRAINT chk_tenant_api_keys_scope CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = 0)
    );
CREATE INDEX idx_tenant_api_keys_scope_type ON tenant_api_keys(scope_type);

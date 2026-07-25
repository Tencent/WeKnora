ALTER TABLE tenant_api_keys
    ADD COLUMN scope_type VARCHAR(16) NOT NULL DEFAULT 'tenant';

ALTER TABLE tenant_api_keys
    MODIFY COLUMN tenant_id BIGINT NULL;

ALTER TABLE tenant_api_keys
    ADD CONSTRAINT chk_tenant_api_keys_scope CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = FALSE)
    );

CREATE INDEX idx_tenant_api_keys_scope_type
    ON tenant_api_keys(scope_type);

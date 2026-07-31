DELETE FROM tenant_api_keys WHERE scope_type = 'platform';

DROP INDEX idx_tenant_api_keys_scope_type ON tenant_api_keys;

ALTER TABLE tenant_api_keys
    DROP CHECK chk_tenant_api_keys_scope;

ALTER TABLE tenant_api_keys
    MODIFY COLUMN tenant_id BIGINT NOT NULL;

ALTER TABLE tenant_api_keys
    DROP COLUMN scope_type;

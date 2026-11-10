DELETE FROM tenant_api_keys WHERE scope_type = 'platform';

DROP INDEX IF EXISTS idx_tenant_api_keys_scope_type;

ALTER TABLE tenant_api_keys
    DROP CONSTRAINT IF EXISTS chk_tenant_api_keys_scope;

ALTER TABLE tenant_api_keys
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE tenant_api_keys
    DROP COLUMN IF EXISTS scope_type;

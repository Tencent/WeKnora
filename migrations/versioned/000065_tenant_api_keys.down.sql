ALTER TABLE tenants ADD COLUMN IF NOT EXISTS api_key VARCHAR(256) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_tenants_api_key ON tenants(api_key);
DROP INDEX IF EXISTS idx_tenant_api_keys_revoked_at;
DROP INDEX IF EXISTS idx_tenant_api_keys_tenant;
DROP TABLE IF EXISTS tenant_api_keys;

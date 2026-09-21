ALTER TABLE tenant_api_keys ADD COLUMN legacy_session_key_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tenant_api_keys ADD COLUMN api_principal_config TEXT;
ALTER TABLE tenant_api_keys ADD COLUMN identity_namespace TEXT NOT NULL DEFAULT '';
UPDATE tenant_api_keys SET api_principal_config = COALESCE(
 (SELECT t.api_principal_config FROM tenants t WHERE t.id = tenant_api_keys.tenant_id), '{"mode":"tenant"}')
WHERE scope_type = 'tenant';

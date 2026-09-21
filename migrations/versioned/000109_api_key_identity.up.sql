ALTER TABLE tenant_api_keys ADD COLUMN legacy_session_key_id BIGINT NOT NULL DEFAULT 0;
-- Snapshot the legacy workspace policy; preserve existing session/OAuth owners.
ALTER TABLE tenant_api_keys ADD COLUMN IF NOT EXISTS api_principal_config JSONB;
ALTER TABLE tenant_api_keys ADD COLUMN IF NOT EXISTS identity_namespace VARCHAR(64) NOT NULL DEFAULT '';
UPDATE tenant_api_keys k SET api_principal_config = COALESCE(t.api_principal_config, '{"mode":"tenant"}'::jsonb)
FROM tenants t WHERE k.tenant_id = t.id AND k.scope_type = 'tenant' AND k.api_principal_config IS NULL;

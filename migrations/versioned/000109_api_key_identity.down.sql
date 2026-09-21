ALTER TABLE tenant_api_keys DROP COLUMN IF EXISTS api_principal_config;
ALTER TABLE tenant_api_keys DROP COLUMN IF EXISTS identity_namespace;
ALTER TABLE tenant_api_keys DROP COLUMN legacy_session_key_id;

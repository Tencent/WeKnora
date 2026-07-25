DO $$ BEGIN RAISE NOTICE '[Migration 000065 down] Restoring tenants.api_key from tenant_api_keys...'; END $$;

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS api_key VARCHAR(256) NOT NULL DEFAULT '';

UPDATE tenants t
SET api_key = sub.api_key
FROM (
    SELECT DISTINCT ON (tenant_id) tenant_id, api_key
    FROM tenant_api_keys
    WHERE revoked_at IS NULL AND COALESCE(api_key, '') <> ''
    ORDER BY tenant_id, created_at DESC
) sub
WHERE t.id = sub.tenant_id;

CREATE INDEX IF NOT EXISTS idx_tenants_api_key ON tenants(api_key);
DROP INDEX IF EXISTS idx_tenant_api_keys_revoked_at;
DROP INDEX IF EXISTS idx_tenant_api_keys_tenant;
DROP TABLE IF EXISTS tenant_api_keys;

DO $$ BEGIN RAISE NOTICE '[Migration 000065 down] tenants.api_key restored'; END $$;

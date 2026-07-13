ALTER TABLE tenants
    ADD COLUMN api_key VARCHAR(256) NOT NULL DEFAULT '' AFTER business;

UPDATE tenants AS t
SET t.api_key = COALESCE((
    SELECT k.api_key
    FROM tenant_api_keys AS k
    WHERE k.tenant_id = t.id
      AND k.revoked_at IS NULL
      AND COALESCE(k.api_key, '') <> ''
    ORDER BY k.created_at DESC, k.id DESC
    LIMIT 1
), '');

CREATE INDEX idx_tenants_api_key ON tenants(api_key);
DROP TABLE tenant_api_keys;

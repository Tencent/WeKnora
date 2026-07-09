ALTER TABLE tenants ADD COLUMN api_key VARCHAR(256) NOT NULL DEFAULT '';

UPDATE tenants t
JOIN (
    SELECT k.tenant_id, k.api_key
    FROM tenant_api_keys k
    JOIN (
        SELECT tenant_id, MAX(id) AS id
        FROM tenant_api_keys
        WHERE revoked_at IS NULL AND COALESCE(api_key, '') <> ''
        GROUP BY tenant_id
    ) latest ON latest.id = k.id
) sub ON sub.tenant_id = t.id
SET t.api_key = sub.api_key;

CREATE INDEX idx_tenants_api_key ON tenants(api_key);
DROP TABLE IF EXISTS tenant_api_keys;

-- Revocation is necessary: dropping per-KB permissions would widen these keys.
UPDATE tenant_api_keys SET revoked_at = CURRENT_TIMESTAMP
WHERE knowledge_base_permissions IS NOT NULL AND revoked_at IS NULL;
ALTER TABLE tenant_api_keys DROP COLUMN IF EXISTS knowledge_base_permissions;

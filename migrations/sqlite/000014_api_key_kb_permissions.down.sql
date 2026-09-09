-- Never widen a granular key when reverting to a schema without these grants.
UPDATE tenant_api_keys SET revoked_at = CURRENT_TIMESTAMP
WHERE knowledge_base_permissions IS NOT NULL AND revoked_at IS NULL;
ALTER TABLE tenant_api_keys DROP COLUMN knowledge_base_permissions;

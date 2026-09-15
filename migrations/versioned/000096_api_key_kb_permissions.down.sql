DO $$ BEGIN RAISE NOTICE '[Migration 000096 down] Dropping tenant_api_keys.knowledge_base_permissions'; END $$;

ALTER TABLE tenant_api_keys DROP COLUMN IF EXISTS knowledge_base_permissions;

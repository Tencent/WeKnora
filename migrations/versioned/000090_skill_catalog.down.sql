DO $$ BEGIN RAISE NOTICE '[Migration 000090 down] Dropping skill catalog'; END $$;

DROP INDEX IF EXISTS idx_tenant_skills_catalog;
ALTER TABLE tenant_skills DROP COLUMN IF EXISTS catalog_id;
DROP TABLE IF EXISTS tenant_skill_catalog;

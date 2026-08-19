DO $$ BEGIN RAISE NOTICE '[Migration 000086 down] Dropping tenant skill tables'; END $$;

DROP TABLE IF EXISTS tenant_skill_snapshots;
DROP TABLE IF EXISTS tenant_skills;

-- Rollback: 000075_org_units

DROP INDEX IF EXISTS idx_knowledge_bases_org_unit;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS org_unit_id;

DROP TABLE IF EXISTS org_unit_members;
DROP TABLE IF EXISTS org_units;

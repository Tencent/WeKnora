DROP INDEX IF EXISTS idx_knowledge_bases_org_unit;
-- SQLite cannot DROP COLUMN safely across all versions; leave column.

DROP TABLE IF EXISTS org_unit_members;
DROP TABLE IF EXISTS org_units;

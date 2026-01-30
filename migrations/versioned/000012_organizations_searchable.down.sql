-- Rollback: 000012_organizations_searchable
ALTER TABLE organizations DROP COLUMN IF EXISTS searchable;

-- Down migration for 000052_models_managed_by.

DROP INDEX IF EXISTS idx_models_managed_by_yaml;

ALTER TABLE models DROP COLUMN IF EXISTS managed_by;

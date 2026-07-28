-- MySQL 8 translation of 000052_models_managed_by.
ALTER TABLE models ADD COLUMN managed_by VARCHAR(32) NOT NULL DEFAULT '';
CREATE INDEX idx_models_managed_by_yaml ON models(managed_by);

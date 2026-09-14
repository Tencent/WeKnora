DROP INDEX IF EXISTS idx_system_settings_category;
DROP TABLE IF EXISTS system_settings;

-- SQLite does not support DROP COLUMN before 3.35; Lite dev DBs can be recreated.
-- No-op down migration for additive columns.

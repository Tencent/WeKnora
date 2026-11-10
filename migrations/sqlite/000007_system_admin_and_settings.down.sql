DROP INDEX IF EXISTS idx_system_settings_category;
DROP TABLE IF EXISTS system_settings;
DROP INDEX IF EXISTS idx_users_is_system_admin;
ALTER TABLE users DROP COLUMN is_system_admin;

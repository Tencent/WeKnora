DROP INDEX IF EXISTS idx_platform_auth_providers_kind;
DROP TABLE IF EXISTS platform_auth_providers;
ALTER TABLE users DROP COLUMN auth_source;

ALTER TABLE users DROP COLUMN auth_source;
DROP TABLE IF EXISTS we_chat_auth_settings;
DROP TABLE IF EXISTS we_chat_login_attempts;
DROP TABLE IF EXISTS external_identities;

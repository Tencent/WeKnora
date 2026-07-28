-- MySQL 8 translation of 000049_user_preferences.
-- JSON defaults must be expressions in MySQL. GORM omits the zero-value
-- Preferences field on insert because its model declares a database default.
ALTER TABLE users
    ADD COLUMN preferences JSON NOT NULL DEFAULT (JSON_OBJECT());

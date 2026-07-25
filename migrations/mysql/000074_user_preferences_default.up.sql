-- MySQL compatibility repair for databases initialized before 000049 used a
-- JSON default expression. GORM omits zero-value Preferences on user inserts.
ALTER TABLE users
    MODIFY COLUMN preferences JSON NOT NULL DEFAULT (JSON_OBJECT());

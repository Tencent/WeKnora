-- Keep the explicit empty-object default required by the User model.
ALTER TABLE users
    ALTER COLUMN preferences SET DEFAULT '{}'::jsonb;

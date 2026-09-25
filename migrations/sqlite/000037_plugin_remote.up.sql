-- Remote plugin URL and signing secret (versioned 000118).
ALTER TABLE plugins ADD COLUMN remote_url VARCHAR(1024) NOT NULL DEFAULT '';
ALTER TABLE plugins ADD COLUMN remote_secret TEXT NOT NULL DEFAULT '';

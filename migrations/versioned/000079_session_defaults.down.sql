-- Version 78 already had these PostgreSQL defaults, so rolling back the
-- MySQL-specific compatibility repair must preserve the PostgreSQL schema.
ALTER TABLE sessions
    ALTER COLUMN fallback_response
        SET DEFAULT '很抱歉，我暂时无法回答这个问题。',
    ALTER COLUMN summary_parameters
        SET DEFAULT '{}'::jsonb;

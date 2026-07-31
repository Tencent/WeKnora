-- PostgreSQL already carried these defaults in the baseline. Reassert them so
-- dialect migration heads stay aligned with the MySQL compatibility repair.
ALTER TABLE sessions
    ALTER COLUMN fallback_response
        SET DEFAULT '很抱歉，我暂时无法回答这个问题。',
    ALTER COLUMN summary_parameters
        SET DEFAULT '{}'::jsonb;

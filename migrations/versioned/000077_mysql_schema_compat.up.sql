-- Reassert base-table defaults needed by insert paths that omit optional
-- session fields. MySQL carries the companion table repair in its dialect
-- migration with the same version.
ALTER TABLE sessions
    ALTER COLUMN fallback_response SET DEFAULT '很抱歉，我暂时无法回答这个问题。';
ALTER TABLE sessions
    ALTER COLUMN summary_parameters SET DEFAULT '{}'::jsonb;

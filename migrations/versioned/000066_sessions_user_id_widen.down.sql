-- Migration: 000066_sessions_user_id_widen (down)
-- Note: fails if any row has user_id longer than 36 characters.

ALTER TABLE sessions
    ALTER COLUMN user_id TYPE VARCHAR(36);

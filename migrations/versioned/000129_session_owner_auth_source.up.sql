-- Migration: 000129_session_owner_auth_source
-- sessions.user_id names the owner scope but not how the server authenticated
-- it. Per-user sandbox allocation may only share a machine between sessions
-- of a verified person (a web account or an API external user), so the
-- authentication source is recorded server-side when the session is created
-- or its owner is first bound; the owner's kind follows from the source and
-- the shape of user_id. Existing rows stay '' and are treated as unknown:
-- they keep a sandbox per session forever.
DO $$ BEGIN RAISE NOTICE '[Migration 000129] Adding sessions.owner_auth_source'; END $$;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS owner_auth_source VARCHAR(32) NOT NULL DEFAULT '';

COMMENT ON COLUMN sessions.owner_auth_source IS 'Server-verified authentication source of user_id; empty = unknown';

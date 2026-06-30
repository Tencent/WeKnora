-- Migration: 000066_sessions_user_id_widen
-- Description: Widen sessions.user_id to store principal-based owner IDs
--              (embed_session, api_external_user, etc.), not only UUIDs.

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Widening sessions.user_id to VARCHAR(512)'; END $$;

ALTER TABLE sessions
    ALTER COLUMN user_id TYPE VARCHAR(512);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] sessions.user_id widened'; END $$;

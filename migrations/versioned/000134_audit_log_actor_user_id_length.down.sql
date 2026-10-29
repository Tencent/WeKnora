-- Migration 000134 down: narrow audit_logs.actor_user_id back to VARCHAR(36).
--
-- This only succeeds when every stored value still fits in 36 chars. Any
-- embed-channel row ("embed-<channel_id>", 42 chars) would violate the
-- narrower type, so the ALTER aborts rather than truncating data.
DO $$ BEGIN RAISE NOTICE '[Migration 000134 down] Narrowing audit_logs.actor_user_id back to VARCHAR(36)...'; END $$;

ALTER TABLE audit_logs
    ALTER COLUMN actor_user_id TYPE VARCHAR(36);

DO $$ BEGIN RAISE NOTICE '[Migration 000134 down] audit_logs.actor_user_id narrowed'; END $$;

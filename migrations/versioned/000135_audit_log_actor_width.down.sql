-- Migration: 000135_audit_log_actor_width (down)
-- Narrowing back is lossy: synthetic actor ids longer than 36 characters are
-- truncated by the USING clause so the ALTER cannot fail on existing rows.
DO $$ BEGIN RAISE NOTICE '[Migration 000135 down] Narrowing audit_logs.actor_user_id to VARCHAR(36)'; END $$;

ALTER TABLE audit_logs
    ALTER COLUMN actor_user_id TYPE VARCHAR(36) USING LEFT(actor_user_id, 36);

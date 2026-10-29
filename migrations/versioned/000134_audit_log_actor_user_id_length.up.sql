-- Migration: 000134_audit_log_actor_user_id_length
-- Widens audit_logs.actor_user_id from VARCHAR(36) to VARCHAR(64).
--
-- Regular user IDs are 36-char UUIDs, but the embed widget channel
-- synthesizes an actor identity as "embed-<channel_id>" (6 + 36 = 42
-- chars). Those rows previously failed with SQLSTATE 22001 ("value too
-- long for type character varying(36)"), so every embed-driven audit
-- event was silently dropped. VARCHAR(64) keeps the existing UUID rows
-- intact while giving the synthetic embed prefix room to fit.
DO $$ BEGIN RAISE NOTICE '[Migration 000134] Widening audit_logs.actor_user_id to VARCHAR(64)...'; END $$;

ALTER TABLE audit_logs
    ALTER COLUMN actor_user_id TYPE VARCHAR(64);

DO $$ BEGIN RAISE NOTICE '[Migration 000134] audit_logs.actor_user_id widened'; END $$;

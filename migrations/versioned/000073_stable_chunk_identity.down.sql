DO $$ BEGIN RAISE NOTICE '[Migration 000073 down] Removing stable chunk identity columns...'; END $$;

DROP INDEX IF EXISTS idx_chunks_stable_identity;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS identity_version;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS stable_identity;

DO $$ BEGIN RAISE NOTICE '[Migration 000073 down] Stable chunk identity columns removed'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000073] Adding stable chunk identity columns...'; END $$;

ALTER TABLE chunks
    ADD COLUMN IF NOT EXISTS stable_identity VARCHAR(36);

ALTER TABLE chunks
    ADD COLUMN IF NOT EXISTS identity_version VARCHAR(32);

-- This index is intentionally non-unique. Rebuild currently soft-deletes old
-- rows before inserting new random row IDs, so historical and active rows may
-- temporarily or permanently share the same stable business identity.
CREATE INDEX IF NOT EXISTS idx_chunks_stable_identity
    ON chunks(tenant_id, knowledge_id, stable_identity);

DO $$ BEGIN RAISE NOTICE '[Migration 000073] Stable chunk identity columns ready'; END $$;

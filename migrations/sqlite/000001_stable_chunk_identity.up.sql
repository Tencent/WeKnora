ALTER TABLE chunks ADD COLUMN stable_identity VARCHAR(36);
ALTER TABLE chunks ADD COLUMN identity_version VARCHAR(32);

-- Non-unique by design: soft-deleted history may share a stable identity with
-- the newly rebuilt active row while database row IDs remain random UUIDs.
CREATE INDEX IF NOT EXISTS idx_chunks_stable_identity
    ON chunks(tenant_id, knowledge_id, stable_identity);

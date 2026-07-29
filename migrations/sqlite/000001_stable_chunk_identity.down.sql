DROP INDEX IF EXISTS idx_chunks_stable_identity;
ALTER TABLE chunks DROP COLUMN identity_version;
ALTER TABLE chunks DROP COLUMN stable_identity;

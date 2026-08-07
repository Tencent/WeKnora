-- Roll back the internal embeddings access-metadata storage when present.
DROP INDEX IF EXISTS idx_embeddings_access_metadata;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embeddings') THEN
        ALTER TABLE embeddings DROP COLUMN IF EXISTS access_metadata;
    END IF;
END $$;

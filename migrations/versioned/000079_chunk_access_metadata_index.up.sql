-- Store internal chunk access constraints alongside embeddings when PostgreSQL
-- retrieval is enabled. Deployments that skip the conditional embeddings setup
-- do not have this table and must safely skip this migration.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embeddings') THEN
        ALTER TABLE embeddings
            ADD COLUMN IF NOT EXISTS access_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

        CREATE INDEX IF NOT EXISTS idx_embeddings_access_metadata
            ON embeddings USING GIN (access_metadata);
    ELSE
        RAISE NOTICE '[Migration 000079] embeddings table does not exist, skipping';
    END IF;
END $$;

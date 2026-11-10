-- Remove tag_id column from embeddings table
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'embeddings' AND column_name = 'tag_id'
    ) THEN
        DROP INDEX IF EXISTS idx_embeddings_tag_id;
        ALTER TABLE embeddings DROP COLUMN tag_id;
        RAISE NOTICE '[Migration 000007 Rollback] Removed tag_id column from embeddings table';
    END IF;
END $$;

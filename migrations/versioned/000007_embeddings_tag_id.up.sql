-- Add tag_id column to embeddings table for FAQ priority filtering
DO $$
BEGIN
    -- Check if table exists first
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embeddings') THEN
        -- Add tag_id column if not exists
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns 
            WHERE table_name = 'embeddings' AND column_name = 'tag_id'
        ) THEN
            ALTER TABLE embeddings ADD COLUMN tag_id VARCHAR(36);
            CREATE INDEX IF NOT EXISTS idx_embeddings_tag_id ON embeddings(tag_id);
            RAISE NOTICE '[Migration 000007] Added tag_id column and index to embeddings table';
        ELSE
            RAISE NOTICE '[Migration 000007] tag_id column already exists in embeddings table, skipping';
        END IF;
    ELSE
        RAISE NOTICE '[Migration 000007] embeddings table does not exist, skipping';
    END IF;
END $$;

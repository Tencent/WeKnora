-- Reverse 000095. The BYTEA vector is untouched, so dropping the derived
-- column only sends ranking back into the application.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables WHERE table_name = 'memory_item_embeddings'
    ) THEN
        ALTER TABLE memory_item_embeddings DROP COLUMN IF EXISTS embedding;
        DROP INDEX IF EXISTS idx_mem_emb_search;
    END IF;
END $$;

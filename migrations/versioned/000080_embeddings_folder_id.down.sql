-- Migration: 000080_embeddings_folder_id (down)
DO $$
DECLARE
    target_schema TEXT := current_schema();
BEGIN
    EXECUTE format('DROP INDEX IF EXISTS %I.idx_embeddings_folder_id', target_schema);
    EXECUTE format(
        'ALTER TABLE IF EXISTS %I.embeddings DROP COLUMN IF EXISTS folder_id',
        target_schema
    );
END $$;

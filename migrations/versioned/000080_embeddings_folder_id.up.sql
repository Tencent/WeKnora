-- Migration: 000080_embeddings_folder_id
-- Description: Add folder_id column to the embeddings table so vector-store
-- chunks can carry the document folder they were filed under (issue #1311).
-- Mirrors migration 000007_embeddings_tag_id in shape.

DO $$
DECLARE
    target_schema TEXT := current_schema();
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = target_schema AND table_name = 'embeddings'
    ) THEN
        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = target_schema
              AND table_name = 'embeddings'
              AND column_name = 'folder_id'
        ) THEN
            EXECUTE format(
                $sql$ALTER TABLE %I.embeddings
                    ADD COLUMN folder_id VARCHAR(36) NOT NULL DEFAULT ''$sql$,
                target_schema
            );
            RAISE NOTICE '[Migration 000080] Added folder_id column to embeddings table';
        ELSE
            RAISE NOTICE '[Migration 000080] folder_id column already exists in embeddings table';
        END IF;
        EXECUTE format(
            'CREATE INDEX IF NOT EXISTS idx_embeddings_folder_id ON %I.embeddings(folder_id)',
            target_schema
        );
        RAISE NOTICE '[Migration 000080] Ensured folder_id index on embeddings table';
    ELSE
        RAISE NOTICE '[Migration 000080] embeddings table does not exist, skipping';
    END IF;
END $$;

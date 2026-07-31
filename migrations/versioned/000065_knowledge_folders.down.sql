-- Migration 000065: Knowledge Folders (rollback)

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Rolling back knowledge folders...'; END $$;

ALTER TABLE knowledges
    DROP CONSTRAINT IF EXISTS fk_knowledge_folder;

DROP INDEX IF EXISTS idx_knowledges_folder;

ALTER TABLE knowledges
    DROP COLUMN IF EXISTS folder_id;

DROP TABLE IF EXISTS knowledge_folders CASCADE;

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Knowledge folders rollback complete'; END $$;

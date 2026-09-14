-- Rollback: 000077_kb_share_with_descendants

DO $$ BEGIN RAISE NOTICE '[Migration 000077 DOWN] Dropping share_with_descendants...'; END $$;

ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS share_with_descendants;

DO $$ BEGIN RAISE NOTICE '[Migration 000077 DOWN] Done'; END $$;

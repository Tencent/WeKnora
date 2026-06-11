-- Migration: 000064_embed_channels_drop_kb_id
-- Remove deprecated knowledge_base_id; KB scope comes from agent config only.
DO $$ BEGIN RAISE NOTICE '[Migration 000064] Dropping embed_channels.knowledge_base_id'; END $$;

DROP INDEX IF EXISTS idx_embed_channels_kb;
ALTER TABLE embed_channels DROP COLUMN IF EXISTS knowledge_base_id;

DO $$ BEGIN RAISE NOTICE '[Migration 000064] Done'; END $$;

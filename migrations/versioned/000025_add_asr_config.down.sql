-- Migration: 000025_add_asr_config (rollback)
-- Description: Remove asr_config column from knowledge_bases.
DO $$ BEGIN RAISE NOTICE '[Migration 000025] Removing asr_config column from knowledge_bases'; END $$;

ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS asr_config;

DO $$ BEGIN RAISE NOTICE '[Migration 000025] asr_config column removed successfully'; END $$;

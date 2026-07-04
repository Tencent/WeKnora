-- Migration: 000039_vlm_cache (rollback)
-- Description: Drop vlm_cache table.
DO $$ BEGIN RAISE NOTICE '[Migration 000039 DOWN] Dropping vlm_cache table'; END $$;
DROP TABLE IF EXISTS vlm_cache
DO $$ BEGIN RAISE NOTICE '[Migration 000039 DOWN] vlm_cache table dropped successfully'; END $$;
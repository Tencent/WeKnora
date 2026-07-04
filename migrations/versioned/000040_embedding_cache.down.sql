DO $$ BEGIN RAISE NOTICE '[Migration: 000040] Dropping embedding_cache table';END $$;
DROP TABLE IF EXISTS embedding_cache;
DO $$ BEGIN RAISE NOTICE '[Migration 000040] embedding_cache table dropped successfully'; END $$;
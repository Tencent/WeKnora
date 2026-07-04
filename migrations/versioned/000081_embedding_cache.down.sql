DO $$ BEGIN RAISE NOTICE '[Migration: 000081] Dropping embedding_cache table';END $$;
DROP TABLE IF EXISTS embedding_cache;
DO $$ BEGIN RAISE NOTICE '[Migration 000081] embedding_cache table dropped successfully'; END $$;
-- Migration: chunk_flags (rollback)
-- Description: Remove flags column from chunks table

DO $$ 
BEGIN
    RAISE NOTICE '[Migration 000003] Removing flags column from chunks table...';
    
    ALTER TABLE chunks DROP COLUMN IF EXISTS flags;
    
    RAISE NOTICE '[Migration 000003] Flags column removed successfully';
END $$;

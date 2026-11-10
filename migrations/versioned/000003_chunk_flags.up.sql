-- Migration: chunk_flags
-- Description: Add flags column to chunks table for managing multiple boolean states

DO $$ 
BEGIN
    RAISE NOTICE '[Migration 000003] Adding flags column to chunks table...';
    
    -- Add flags column with default value 1 (ChunkFlagRecommended)
    -- This means all existing chunks will be recommended by default
    ALTER TABLE chunks ADD COLUMN IF NOT EXISTS flags INTEGER NOT NULL DEFAULT 1;
    
    RAISE NOTICE '[Migration 000003] Flags column added successfully';
END $$;

-- Migration: drop_vlm_model_id
-- Description: Drop vlm_model_id column from knowledge_bases table (field moved to vlm_config JSON)

DO $$ 
BEGIN
    RAISE NOTICE '[Migration 000004] Dropping vlm_model_id column from knowledge_bases table...';
    
    -- Drop vlm_model_id column if exists (this field was moved to vlm_config JSON)
    ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS vlm_model_id;
    
    RAISE NOTICE '[Migration 000004] vlm_model_id column dropped successfully';
END $$;

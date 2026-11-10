-- Migration: drop_vlm_model_id (rollback)
-- Description: Re-add vlm_model_id column to knowledge_bases table

DO $$ 
BEGIN
    RAISE NOTICE '[Migration 000004 Rollback] Re-adding vlm_model_id column to knowledge_bases table...';
    
    -- Re-add vlm_model_id column (nullable to avoid issues)
    ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS vlm_model_id VARCHAR(64);
    
    RAISE NOTICE '[Migration 000004 Rollback] vlm_model_id column re-added successfully';
END $$;

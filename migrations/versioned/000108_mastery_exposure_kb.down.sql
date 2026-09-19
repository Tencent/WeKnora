-- Migration 000108 down: drop the knowledge_base_id column from exposures.

ALTER TABLE memory_guide_exposures
    DROP COLUMN IF EXISTS knowledge_base_id;

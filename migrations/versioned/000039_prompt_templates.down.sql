-- Migration: 000039_prompt_templates (rollback)
-- Description: Drop the prompt_templates table. Service falls back to YAML defaults.
DO $$ BEGIN RAISE NOTICE '[Migration 000039 DOWN] Dropping prompt_templates table'; END $$;

DROP INDEX IF EXISTS idx_prompt_templates_updated_at;
DROP INDEX IF EXISTS idx_prompt_templates_category;
DROP TABLE IF EXISTS prompt_templates;

DO $$ BEGIN RAISE NOTICE '[Migration 000039 DOWN] prompt_templates table dropped'; END $$;

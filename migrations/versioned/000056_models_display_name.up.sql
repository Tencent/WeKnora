-- Migration: 000056_models_display_name
-- Add an optional user-facing display name for model rows. Runtime model
-- calls continue to use models.name; this field is presentation-only.

DO $$ BEGIN RAISE NOTICE '[Migration 000056] Adding models.display_name column'; END $$;

ALTER TABLE models
    ADD COLUMN IF NOT EXISTS display_name VARCHAR(255) NOT NULL DEFAULT '';

DO $$ BEGIN RAISE NOTICE '[Migration 000056] Done'; END $$;

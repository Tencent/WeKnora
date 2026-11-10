-- Migration 000101: structured document profiles and generated knowledge-base descriptions.
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS profile JSONB;
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS profile_config JSONB;
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS generated_profile JSONB;

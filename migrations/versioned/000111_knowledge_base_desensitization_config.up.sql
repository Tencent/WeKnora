-- Migration 000111: opt-in per-KB document desensitization configuration.
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS desensitization_config JSONB;

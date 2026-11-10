-- Migration 000080: opt-in automatic association of existing knowledge-base tags.
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS auto_tag_config JSONB;

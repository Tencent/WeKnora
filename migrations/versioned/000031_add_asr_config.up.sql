-- Migration: 000031_add_asr_config
-- Description: Add asr_config column to knowledge_bases for ASR (Automatic Speech Recognition) configuration.
-- ASR config stores the model ID and language settings for audio transcription.
DO $$ BEGIN RAISE NOTICE '[Migration 000031] Adding asr_config column to knowledge_bases'; END $$;

ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS asr_config JSONB;

COMMENT ON COLUMN knowledge_bases.asr_config IS 'ASR (Automatic Speech Recognition) configuration: {"enabled": bool, "model_id": string, "language": string}';

DO $$ BEGIN RAISE NOTICE '[Migration 000031] asr_config column added successfully'; END $$;

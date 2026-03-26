-- Migration: 000027_message_rendered_content
-- Description: Add rendered_content column to messages to persist RAG-augmented user content for multi-turn context
DO $$ BEGIN RAISE NOTICE '[Migration 000027] Adding rendered_content column to messages'; END $$;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS rendered_content TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN messages.rendered_content IS 'Full RAG-augmented user message sent to LLM, preserving retrieval context across turns';

DO $$ BEGIN RAISE NOTICE '[Migration 000027] rendered_content column added to messages successfully'; END $$;

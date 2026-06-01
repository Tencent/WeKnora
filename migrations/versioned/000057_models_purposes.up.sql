-- Migration: 000057_models_purposes
-- Add a soft-tag list of intended usage roles to the models table.
--
-- Background:
--   * The `type` column already disambiguates the API contract a model
--     speaks (Embedding / Rerank / KnowledgeQA / VLLM / ASR), but it
--     can't express the role a model is *recommended for* within a
--     given contract — e.g. two KnowledgeQA-typed chat models, one
--     tuned for user-facing Q&A and one optimized for wiki page
--     generation, both speak the same chat-completion API.
--   * Operators currently have to encode the intended role in the
--     model name, which selectors can't reliably parse.
--
-- The `purposes` column stores a JSONB array of free-form string tags
-- (e.g. ["qa"], ["wiki-synthesis"], ["qa", "wiki-synthesis"]). It is
-- intentionally open-ended so new roles can be added without enum or
-- schema changes — selectors interpret an empty/NULL value as "no
-- preference" and fall back to type-based selection.
DO $$ BEGIN RAISE NOTICE '[Migration 000057] Adding models.purposes'; END $$;

ALTER TABLE models
    ADD COLUMN IF NOT EXISTS purposes JSONB;

DO $$ BEGIN RAISE NOTICE '[Migration 000057] models.purposes added'; END $$;

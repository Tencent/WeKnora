-- Migration 000113: record which knowledge base a guidance exposure belongs to.
--
-- The exposure log originally keyed de-duplication on (tenant, subject,
-- candidate_slug), so the same slug in two different knowledge bases would
-- collide. Adding knowledge_base_id lets click-through and effective-view
-- evaluation scope each candidate to its own KB.

ALTER TABLE memory_guide_exposures
    ADD COLUMN IF NOT EXISTS knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '';


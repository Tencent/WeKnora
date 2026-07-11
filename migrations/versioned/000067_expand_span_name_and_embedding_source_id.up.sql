-- Migration: widen dynamic processing identifiers.
-- Wiki page span names include a slug, while generated-question source IDs
-- combine two UUIDs; both legitimately exceed the original 64 characters.

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(256);

ALTER TABLE IF EXISTS embeddings
    ALTER COLUMN source_id TYPE VARCHAR(128);

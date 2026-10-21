DROP INDEX IF EXISTS embeddings_textsearch_idx;
ALTER TABLE IF EXISTS embeddings DROP COLUMN IF EXISTS search_content;

-- Migration 000010 Down: Remove seq_id from chunks and knowledge_tags tables

-- Remove seq_id from chunks
DROP INDEX IF EXISTS idx_chunks_seq_id;
ALTER TABLE chunks DROP COLUMN IF EXISTS seq_id;
DROP SEQUENCE IF EXISTS chunks_seq_id_seq;

-- Remove seq_id from knowledge_tags
DROP INDEX IF EXISTS idx_knowledge_tags_seq_id;
ALTER TABLE knowledge_tags DROP COLUMN IF EXISTS seq_id;
DROP SEQUENCE IF EXISTS knowledge_tags_seq_id_seq;

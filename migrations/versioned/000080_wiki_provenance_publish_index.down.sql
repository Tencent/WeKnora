-- Migration: 000080_wiki_provenance_publish_index (rollback)

DROP INDEX IF EXISTS idx_knowledge_revisions_content_hash;

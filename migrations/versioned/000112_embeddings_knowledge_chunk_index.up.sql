-- Migration 000112: btree indexes on embeddings(knowledge_id) and (chunk_id)
--
-- Deleting a document (DeleteByKnowledgeIDList), deleting or re-indexing
-- chunks (DeleteByChunkIDList), toggling chunk enabled state and moving chunk
-- tags (BatchUpdateChunkEnabledStatus / BatchUpdateChunkTagID) all filter
-- embeddings by knowledge_id or chunk_id, and so does vector retrieval scoped
-- to documents. Neither column was indexed, so each of these scanned the
-- whole table — every workspace's rows, each carrying its vector.
--
-- Guarded on the table's existence like 000007 / 000059: deployments whose
-- RETRIEVE_DRIVER excludes postgres never create it. CONCURRENTLY is not used
-- because it cannot run inside the DO block; on a very large table an
-- operator can build the same indexes with CREATE INDEX CONCURRENTLY first,
-- and IF NOT EXISTS makes this migration a no-op afterwards.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embeddings') THEN
        CREATE INDEX IF NOT EXISTS idx_embeddings_knowledge_id ON embeddings(knowledge_id);
        CREATE INDEX IF NOT EXISTS idx_embeddings_chunk_id ON embeddings(chunk_id);
        RAISE NOTICE '[Migration 000112] Created knowledge_id and chunk_id indexes on embeddings';
    ELSE
        RAISE NOTICE '[Migration 000112] embeddings table does not exist · skipping';
    END IF;
END $$;

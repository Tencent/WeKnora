-- Migration: 000082_wiki_and_graph_cache
-- Description: Drop wiki_doc_map_cache and graph_chunk_cache tables

DO $$ BEGIN RAISE NOTICE '[Migration 000082] Dropping wiki_doc_map_cache and graph_chunk_cache tables'; END $$;

DROP TABLE IF EXISTS wiki_doc_map_cache;
DROP TABLE IF EXISTS graph_chunk_cache;

DO $$ BEGIN RAISE NOTICE '[Migration 000082] wiki_doc_map_cache and graph_chunk_cache tables dropped successfully'; END $$;

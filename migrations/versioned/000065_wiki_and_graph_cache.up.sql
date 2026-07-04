-- Migration: 000065_wiki_and_graph_cache
-- Description: Create wiki_doc_map_cache and graph_chunk_cache tables for
-- PR-4 Layer 3 (Wiki per-doc map) and Layer 4 (Graph per-chunk extraction).

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating wiki_doc_map_cache and graph_chunk_cache tables'; END $$;

CREATE TABLE IF NOT EXISTS wiki_doc_map_cache (
    content_hash VARCHAR(64) NOT NULL,
    granularity VARCHAR(32) NOT NULL,
    chat_model_id VARCHAR(128) NOT NULL,
    prompt_version VARCHAR(32) NOT NULL DEFAULT 'v1',
    mapped_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (content_hash, granularity, chat_model_id, prompt_version)
);

CREATE INDEX IF NOT EXISTS idx_wiki_map_cache_lru ON wiki_doc_map_cache(last_accessed_at);

CREATE TABLE IF NOT EXISTS graph_chunk_cache (
    content_hash VARCHAR(64) NOT NULL,
    chat_model_id VARCHAR(128) NOT NULL,
    prompt_version VARCHAR(32) NOT NULL DEFAULT 'v1',
    entities_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (content_hash, chat_model_id, prompt_version)
);

CREATE INDEX IF NOT EXISTS idx_graph_chunk_cache_lru ON graph_chunk_cache(last_accessed_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] wiki_doc_map_cache and graph_chunk_cache tables created successfully'; END $$;

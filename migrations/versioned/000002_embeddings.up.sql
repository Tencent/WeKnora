-- Migration: embeddings (conditional)
-- Description: Create embeddings table and indexes (only for postgres retrieve driver)

DO $$
BEGIN
    -- Check if we should skip this migration
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RAISE NOTICE 'Skipping migration embeddings (app.skip_embedding=true)';
        RETURN;
    END IF;

    -- If we reach here, proceed with migration
    RAISE NOTICE '[Conditional Migration: embeddings] Creating embeddings table...';

    -- Create required extensions
    CREATE EXTENSION IF NOT EXISTS vector;
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
    CREATE EXTENSION IF NOT EXISTS pg_search;

    -- Create embeddings table
    RAISE NOTICE '[Conditional Migration: embeddings] Creating indexes for embeddings (this may take a while)...';
    
    CREATE TABLE IF NOT EXISTS embeddings (
        id SERIAL PRIMARY KEY,
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

        source_id VARCHAR(64) NOT NULL,
        source_type INTEGER NOT NULL,
        chunk_id VARCHAR(64),
        knowledge_id VARCHAR(64),
        knowledge_base_id VARCHAR(64),
        content TEXT,
        dimension INTEGER NOT NULL,
        embedding halfvec
    );

    CREATE UNIQUE INDEX IF NOT EXISTS embeddings_unique_source ON embeddings(source_id, source_type);

    -- Create BM25 search index (check if exists first)
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'embeddings_search_idx') THEN
        CREATE INDEX embeddings_search_idx ON embeddings
        USING bm25 (id, knowledge_base_id, content, knowledge_id, chunk_id)
        WITH (
            key_field = 'id',
            text_fields = '{
                "content": {
                  "tokenizer": {"type": "chinese_lindera"}
                }
            }'
        );
        RAISE NOTICE '[Conditional Migration: embeddings] Created BM25 index embeddings_search_idx';
    ELSE
        RAISE NOTICE '[Conditional Migration: embeddings] BM25 index embeddings_search_idx already exists';
    END IF;

    -- Create HNSW indexes for vector search (check if exists first)
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'embeddings_embedding_idx' OR indexname LIKE 'embeddings_embedding%3584%') THEN
        CREATE INDEX embeddings_embedding_idx_3584 ON embeddings 
        USING hnsw ((embedding::halfvec(3584)) halfvec_cosine_ops) 
        WITH (m = 16, ef_construction = 64) 
        WHERE (dimension = 3584);
        RAISE NOTICE '[Conditional Migration: embeddings] Created HNSW index for dimension 3584';
    ELSE
        RAISE NOTICE '[Conditional Migration: embeddings] HNSW index for dimension 3584 already exists';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'embeddings_embedding_idx_798' OR indexname LIKE 'embeddings_embedding%798%') THEN
        CREATE INDEX embeddings_embedding_idx_798 ON embeddings 
        USING hnsw ((embedding::halfvec(798)) halfvec_cosine_ops) 
        WITH (m = 16, ef_construction = 64) 
        WHERE (dimension = 798);
        RAISE NOTICE '[Conditional Migration: embeddings] Created HNSW index for dimension 798';
    ELSE
        RAISE NOTICE '[Conditional Migration: embeddings] HNSW index for dimension 798 already exists';
    END IF;

    RAISE NOTICE '[Migration 000002] Adding embeddings enhancements...';

    -- Add is_enabled column
    ALTER TABLE embeddings ADD COLUMN IF NOT EXISTS is_enabled BOOLEAN DEFAULT TRUE;
    CREATE INDEX IF NOT EXISTS idx_embeddings_is_enabled ON embeddings(is_enabled);

    -- Add index for knowledge_base_id
    CREATE INDEX IF NOT EXISTS idx_embeddings_knowledge_base_id ON embeddings(knowledge_base_id);

    -- Reindex BM25 search index (idempotent - will rebuild if exists)
    IF EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'embeddings_search_idx') THEN
        REINDEX INDEX embeddings_search_idx;
        RAISE NOTICE '[Migration 000002] Reindexed embeddings_search_idx';
    END IF;

    RAISE NOTICE '[Conditional Migration: embeddings] Embeddings table setup completed successfully!';
END $$;

-- Migration 000044: HNSW index for bge-m3 / 1024-dim embeddings
--
-- Upstream's 000002 only created HNSW partial indexes for dim=3584 (OpenAI
-- text-embedding-3-large) and dim=798. Both queries fall through to a
-- sequential scan on the 1024-dim halfvec column when the bound embedder
-- is bge-m3 — which is the default for any on-device deployment using
-- localhub-embedder.
--
-- Symptom: vector recall stays "correct but slow" (seq scan of all rows in
-- the embeddings table per query, ~100-500ms by KB size). Downstream agents
-- that probe latency-sensitive endpoints assumed degradation.
--
-- Fix: same partial-index pattern, dim=1024. Matches the exact expression
-- used in repository.go SearchEmbedding query (embedding::halfvec(1024) <=>
-- $1::halfvec(1024)) so the planner picks it up automatically.
--
-- Note: CONCURRENTLY is not used here because plpgsql DO blocks open an
-- implicit transaction and CREATE INDEX CONCURRENTLY can't run inside one.
-- For a fresh install this is a no-op (empty table); for production
-- backfill the operator should build the index manually using
-- CREATE INDEX CONCURRENTLY before applying this migration, then this
-- block becomes idempotent via IF NOT EXISTS.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'embeddings_embedding_idx_1024' OR indexname LIKE 'embeddings_embedding%1024%') THEN
            CREATE INDEX embeddings_embedding_idx_1024 ON embeddings
            USING hnsw ((embedding::halfvec(1024)) halfvec_cosine_ops)
            WITH (m = 16, ef_construction = 64)
            WHERE (dimension = 1024);
            RAISE NOTICE '[Migration 000044] Created HNSW index for dimension 1024 (bge-m3)';
        ELSE
            RAISE NOTICE '[Migration 000044] HNSW index for dimension 1024 already exists';
        END IF;
    ELSE
        RAISE NOTICE '[Migration 000044] pgvector extension not installed · skipping';
    END IF;
END $$;

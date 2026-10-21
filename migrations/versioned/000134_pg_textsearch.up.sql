DO $$
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RETURN;
    END IF;

    -- Nullable derived text leaves existing ParadeDB writes unchanged.
    ALTER TABLE embeddings ADD COLUMN IF NOT EXISTS search_content TEXT;
    IF current_setting('app.postgres_search_extension', true) IS DISTINCT FROM 'pg_textsearch' THEN
        RETURN;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_search') THEN
        RAISE EXCEPTION 'pg_textsearch requires a target database without pg_search; keep the source database unchanged';
    END IF;
    CREATE EXTENSION IF NOT EXISTS pg_textsearch;
    CREATE INDEX IF NOT EXISTS embeddings_textsearch_idx ON embeddings
        USING bm25 (search_content) WITH (text_config = 'simple');
END $$;

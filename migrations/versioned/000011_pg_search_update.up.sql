-- Migration 000011: Update pg_search extension to latest version
-- Equivalent to: psql -c 'ALTER EXTENSION pg_search UPDATE;'

DO $$
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RAISE NOTICE 'Skipping pg_search update (app.skip_embedding=true)';
        RETURN;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_search') THEN
        ALTER EXTENSION pg_search UPDATE;
    END IF;
END $$;

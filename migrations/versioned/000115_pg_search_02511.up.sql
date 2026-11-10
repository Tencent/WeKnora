-- Complete the SQL side of the ParadeDB v0.22.x -> v0.25.11 image upgrade.
-- A catalog left at an older version than the server's shared library can
-- crash the whole server when its SQL functions are called (for example
-- paradedb.version_info()), so the catalog must follow the image.
DO $$
DECLARE
    search_version TEXT;
    vector_version TEXT;
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RETURN;
    END IF;

    -- Leave absent extensions and other release lines under operator control.
    -- Do not downgrade a newer extension or upgrade an external server to its
    -- potentially incompatible default version.
    SELECT extversion INTO search_version FROM pg_extension WHERE extname = 'pg_search';
    IF search_version ~ '^0\.(22|23|24|25)\.[0-9]+$' AND search_version <> '0.25.11'
        AND EXISTS (
            SELECT 1 FROM pg_available_extension_versions
            WHERE name = 'pg_search' AND version = '0.25.11'
        ) THEN
        ALTER EXTENSION pg_search UPDATE TO '0.25.11';
    END IF;

    SELECT extversion INTO vector_version FROM pg_extension WHERE extname = 'vector';
    IF vector_version IN ('0.8.0', '0.8.1', '0.8.2', '0.8.3')
        AND EXISTS (
            SELECT 1 FROM pg_available_extension_versions
            WHERE name = 'vector' AND version = '0.8.4'
        ) THEN
        ALTER EXTENSION vector UPDATE TO '0.8.4';
    END IF;
END $$;

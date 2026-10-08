-- Complete the SQL side of the validated ParadeDB 0.22.2 -> 0.22.6 image upgrade.
-- Earlier extension migrations do not run again on existing databases.
DO $$
DECLARE
    installed_version TEXT;
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RETURN;
    END IF;

    SELECT extversion INTO installed_version FROM pg_extension WHERE extname = 'pg_search';

    -- Leave absent extensions and other release lines under operator control.
    -- Do not downgrade a newer extension or upgrade an external server to its
    -- potentially incompatible default version.
    IF installed_version IS NULL OR installed_version NOT IN ('0.22.2', '0.22.3', '0.22.4', '0.22.5') THEN
        RETURN;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_available_extension_versions
        WHERE name = 'pg_search' AND version = '0.22.6'
    ) THEN
        RAISE NOTICE 'pg_search 0.22.6 is not available; update the server image and run ALTER EXTENSION pg_search UPDATE TO ''0.22.6'' manually';
        RETURN;
    END IF;

    ALTER EXTENSION pg_search UPDATE TO '0.22.6';
END $$;

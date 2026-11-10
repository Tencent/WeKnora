DO $$ BEGIN RAISE NOTICE '[Migration 000072] Converting authorization timestamps to timestamptz...'; END $$;

-- Naive TIMESTAMP values were read through a GORM session with TimeZone=UTC.
-- Treat existing literals as UTC when promoting to timestamptz.
ALTER TABLE tenant_api_keys
    ALTER COLUMN last_used_at TYPE TIMESTAMP WITH TIME ZONE
        USING last_used_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN expires_at TYPE TIMESTAMP WITH TIME ZONE
        USING expires_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN revoked_at TYPE TIMESTAMP WITH TIME ZONE
        USING revoked_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE
        USING updated_at AT TIME ZONE 'UTC';

ALTER TABLE resource_access_grants
    ALTER COLUMN expires_at TYPE TIMESTAMP WITH TIME ZONE
        USING expires_at AT TIME ZONE 'UTC';

ALTER TABLE resource_access_grants
    ALTER COLUMN revoked_at TYPE TIMESTAMP WITH TIME ZONE
        USING revoked_at AT TIME ZONE 'UTC';

ALTER TABLE resource_access_grants
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

DO $$ BEGIN RAISE NOTICE '[Migration 000072] Authorization timestamps ready'; END $$;

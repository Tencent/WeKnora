DO $$ BEGIN RAISE NOTICE '[Migration 000072 down] Reverting authorization timestamps to timestamp...'; END $$;

ALTER TABLE tenant_api_keys
    ALTER COLUMN last_used_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING last_used_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN expires_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING expires_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN revoked_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING revoked_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN created_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

ALTER TABLE tenant_api_keys
    ALTER COLUMN updated_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING updated_at AT TIME ZONE 'UTC';

ALTER TABLE resource_access_grants
    ALTER COLUMN expires_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING expires_at AT TIME ZONE 'UTC';

ALTER TABLE resource_access_grants
    ALTER COLUMN revoked_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING revoked_at AT TIME ZONE 'UTC';

ALTER TABLE resource_access_grants
    ALTER COLUMN created_at TYPE TIMESTAMP WITHOUT TIME ZONE
        USING created_at AT TIME ZONE 'UTC';

DO $$ BEGIN RAISE NOTICE '[Migration 000072 down] Authorization timestamps reverted'; END $$;

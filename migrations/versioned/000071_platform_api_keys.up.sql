DO $$ BEGIN RAISE NOTICE '[Migration 000071] Adding platform API key scope...'; END $$;

ALTER TABLE tenant_api_keys
    ADD COLUMN IF NOT EXISTS scope_type VARCHAR(16) NOT NULL DEFAULT 'tenant';

ALTER TABLE tenant_api_keys
    ALTER COLUMN tenant_id DROP NOT NULL;

ALTER TABLE tenant_api_keys
    DROP CONSTRAINT IF EXISTS chk_tenant_api_keys_scope;

ALTER TABLE tenant_api_keys
    ADD CONSTRAINT chk_tenant_api_keys_scope CHECK (
        (scope_type = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope_type = 'platform' AND tenant_id IS NULL AND full_access = FALSE)
    );

CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_scope_type
    ON tenant_api_keys(scope_type);

DO $$ BEGIN RAISE NOTICE '[Migration 000071] Platform API key scope ready'; END $$;

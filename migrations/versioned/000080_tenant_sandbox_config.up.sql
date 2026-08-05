-- Description: Store multiple named sandbox backend configs per workspace.
-- Replaces the earlier single tenants.tenant_sandbox_config JSONB column: one
-- workspace can now point different agents at different sandbox backends.
DO $$ BEGIN RAISE NOTICE '[Migration 000080] Creating tenant_sandbox_configs'; END $$;

CREATE TABLE IF NOT EXISTS tenant_sandbox_configs (
    id           VARCHAR(36)  PRIMARY KEY,
    tenant_id    BIGINT       NOT NULL,
    name         VARCHAR(255) NOT NULL,
    description  TEXT,
    sandbox_type VARCHAR(32)  NOT NULL,
    config       JSONB        NOT NULL,
    cordoned_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

COMMENT ON COLUMN tenant_sandbox_configs.config IS 'Sandbox backend config (endpoints, encrypted API keys, env vars, volume mount); shape matches types.TenantSandboxConfig';
COMMENT ON COLUMN tenant_sandbox_configs.cordoned_at IS 'Short lease taken while identity fields (provider/URL/API key) are being changed; sandbox resolution is refused while it is fresh';

CREATE INDEX IF NOT EXISTS idx_tenant_sandbox_configs_tenant
    ON tenant_sandbox_configs (tenant_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_sandbox_configs_tenant_name
    ON tenant_sandbox_configs (tenant_id, name) WHERE deleted_at IS NULL;

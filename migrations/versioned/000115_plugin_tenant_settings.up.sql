-- Migration 000115: per-tenant plugin switches and configuration. A plugin
-- without a row is enabled; builtin plugins get rows too once a tenant
-- changes them.
DO $$ BEGIN RAISE NOTICE '[Migration 000115] Creating plugin_tenant_settings'; END $$;

CREATE TABLE IF NOT EXISTS plugin_tenant_settings (
    tenant_id  BIGINT       NOT NULL,
    plugin_id  VARCHAR(128) NOT NULL,
    enabled    BOOLEAN      NOT NULL DEFAULT TRUE,
    config     JSONB,
    updated_by VARCHAR(36)  NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, plugin_id)
);

COMMENT ON TABLE plugin_tenant_settings IS
    'Per-tenant plugin enablement and tenant-level plugin configuration (secrets sealed per the plugin config schema).';

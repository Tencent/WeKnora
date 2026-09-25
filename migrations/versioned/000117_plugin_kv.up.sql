-- Migration 000117: the key-value store plugins use through the Host API.
-- Plugins keep no database of their own; their state lives here, partitioned
-- by plugin and tenant so a plugin cannot read another workspace's data.
DO $$ BEGIN RAISE NOTICE '[Migration 000117] Creating plugin_kv'; END $$;

CREATE TABLE IF NOT EXISTS plugin_kv (
    plugin_id  VARCHAR(128) NOT NULL,
    tenant_id  BIGINT       NOT NULL,
    key        VARCHAR(256) NOT NULL,
    value      JSONB        NOT NULL,
    expires_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, tenant_id, key)
);

CREATE INDEX IF NOT EXISTS idx_plugin_kv_expires_at ON plugin_kv (expires_at) WHERE expires_at IS NOT NULL;

COMMENT ON TABLE plugin_kv IS 'Host API key-value store of plugins, per plugin and tenant.';

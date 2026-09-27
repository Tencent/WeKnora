-- Migration 000124: who an uninstalled plugin was. Its workspaces' switches,
-- configuration, key-value data, OAuth connections and tool policies stay
-- after an uninstall; a later package with the same ID keeps them only if it
-- comes from the same owner and the same trusted signing key.
DO $$ BEGIN RAISE NOTICE '[Migration 000124] Creating plugin_tombstones'; END $$;

CREATE TABLE IF NOT EXISTS plugin_tombstones (
    plugin_id       VARCHAR(128) PRIMARY KEY,
    owner_tenant_id BIGINT,
    signer_key_id   VARCHAR(128) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE plugin_tombstones IS 'Owner and trusted signer of uninstalled plugins, whose data is kept for a reinstall.';

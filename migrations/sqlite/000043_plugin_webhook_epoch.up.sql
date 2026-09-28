-- Generation of a workspace's webhook URLs of a plugin (versioned 000125).
ALTER TABLE plugin_tenant_settings ADD COLUMN webhook_epoch BIGINT NOT NULL DEFAULT 0;

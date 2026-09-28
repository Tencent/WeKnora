-- Migration 000125: a generation for a workspace's webhook URLs of a plugin.
-- The URLs' MAC covers it: bumping it retires every URL the workspace was
-- given for the plugin, without touching anyone else's.
DO $$ BEGIN RAISE NOTICE '[Migration 000125] Adding plugin_tenant_settings.webhook_epoch'; END $$;

ALTER TABLE plugin_tenant_settings ADD COLUMN IF NOT EXISTS webhook_epoch BIGINT NOT NULL DEFAULT 0;

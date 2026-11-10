-- Description: The version of a skill the live image still carries while a
-- newer install of it is in flight or has failed. An install rewrites the row
-- to describe the version being installed as soon as it starts, but the image
-- pointer only moves when that install succeeds, so the previous version keeps
-- running in every sandbox. This column is what lets the agent go on using it
-- instead of losing the skill for the length of the upgrade.
DO $$ BEGIN RAISE NOTICE '[Migration 000104] Adding tenant_skills.served'; END $$;

ALTER TABLE tenant_skills ADD COLUMN IF NOT EXISTS served JSONB;

COMMENT ON COLUMN tenant_skills.served IS
    'Version still in the image while a newer install is pending or failed: {version,description,instructions,bundle_sha256,bundle_ref,snapshot_id}. NULL when the row itself is what is served.';

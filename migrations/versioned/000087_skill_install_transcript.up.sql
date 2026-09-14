-- Description: Locators for the installer agent's transcript. The install runs
-- in a maintenance session whose messages are kept for troubleshooting; without
-- these columns the console has no way to find that conversation.
DO $$ BEGIN RAISE NOTICE '[Migration 000087] Adding install transcript locators to tenant_skills'; END $$;

ALTER TABLE tenant_skills ADD COLUMN IF NOT EXISTS install_session_id VARCHAR(36);
ALTER TABLE tenant_skills ADD COLUMN IF NOT EXISTS install_message_id VARCHAR(36);

COMMENT ON COLUMN tenant_skills.install_session_id IS 'Maintenance session of the most recent install; overwritten on re-install';
COMMENT ON COLUMN tenant_skills.install_message_id IS 'Assistant message the installer transcript was streamed into';

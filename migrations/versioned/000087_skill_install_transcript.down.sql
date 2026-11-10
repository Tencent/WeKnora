DO $$ BEGIN RAISE NOTICE '[Migration 000087] Dropping install transcript locators from tenant_skills'; END $$;

ALTER TABLE tenant_skills DROP COLUMN IF EXISTS install_session_id;
ALTER TABLE tenant_skills DROP COLUMN IF EXISTS install_message_id;

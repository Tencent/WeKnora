-- Description: Pin which sandbox config a session's live sandbox was created on.
-- NULL means "no live sandbox"; '-' means "the deployment-wide default config".
-- This is an ephemeral pin that dies with the sandbox, not a permanent owner.
DO $$ BEGIN RAISE NOTICE '[Migration 000081] Adding sessions.sandbox_config_id'; END $$;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS sandbox_config_id VARCHAR(36) DEFAULT NULL;
COMMENT ON COLUMN sessions.sandbox_config_id IS 'Sandbox config the session current live sandbox was created on; NULL = no live sandbox, ''-'' = deployment default';

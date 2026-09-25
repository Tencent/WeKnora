-- Migration 000118: where a remote plugin runs and the secret its calls are
-- signed with. Only runtime.type remote plugins set them.
DO $$ BEGIN RAISE NOTICE '[Migration 000118] Adding remote plugin columns'; END $$;

ALTER TABLE plugins ADD COLUMN IF NOT EXISTS remote_url VARCHAR(1024) NOT NULL DEFAULT '';
-- Encrypted (enc:v1) HMAC secret shared with the plugin service.
ALTER TABLE plugins ADD COLUMN IF NOT EXISTS remote_secret TEXT NOT NULL DEFAULT '';

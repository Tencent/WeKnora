-- Migration 000120: how far each stored plugin version is trusted, from its
-- package signature: community (unsigned or an unknown key), verified or
-- official (a key in the platform's trust store).
DO $$ BEGIN RAISE NOTICE '[Migration 000120] Adding plugin version trust columns'; END $$;

ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS trust VARCHAR(16) NOT NULL DEFAULT 'community';
-- The key that signed the package, trusted or not; empty when unsigned.
ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS signer_key_id VARCHAR(128) NOT NULL DEFAULT '';

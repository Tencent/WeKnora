-- Plugin version trust level and signing key (versioned 000120).
ALTER TABLE plugin_versions ADD COLUMN trust VARCHAR(16) NOT NULL DEFAULT 'community';
ALTER TABLE plugin_versions ADD COLUMN signer_key_id VARCHAR(128) NOT NULL DEFAULT '';

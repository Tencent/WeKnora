-- Migration 000119: OAuth connections of plugin forms (x-oauth). A form field
-- holds "oauth:<id>"; the tokens stay here, sealed, and WeKnora refreshes
-- them and hands the plugin a fresh access token at call time.
DO $$ BEGIN RAISE NOTICE '[Migration 000119] Creating plugin_oauth_connections'; END $$;

CREATE TABLE IF NOT EXISTS plugin_oauth_connections (
    id           VARCHAR(36)   PRIMARY KEY,
    plugin_id    VARCHAR(128)  NOT NULL,
    tenant_id    BIGINT        NOT NULL,
    scope        VARCHAR(16)   NOT NULL,
    contribution VARCHAR(160)  NOT NULL DEFAULT '',
    field        VARCHAR(256)  NOT NULL,
    token        TEXT          NOT NULL,
    expires_at   TIMESTAMPTZ,
    created_by   VARCHAR(64)   NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_plugin_oauth_connections_owner ON plugin_oauth_connections (plugin_id, tenant_id);

COMMENT ON TABLE plugin_oauth_connections IS 'OAuth tokens behind plugin form fields (x-oauth), sealed.';

-- Migration 000116: installed plugins and their versions. Builtin plugins
-- are compiled in and have no rows here.
DO $$ BEGIN RAISE NOTICE '[Migration 000116] Creating plugins and plugin_versions'; END $$;

CREATE TABLE IF NOT EXISTS plugins (
    id              VARCHAR(128) PRIMARY KEY,
    -- NULL for platform plugins installed by a system admin.
    owner_tenant_id BIGINT,
    source          JSONB        NOT NULL DEFAULT '{}',
    active_version  VARCHAR(64)  NOT NULL,
    -- enabled | disabled: whether the plugin loads on any node.
    desired_state   VARCHAR(16)  NOT NULL DEFAULT 'enabled',
    runtime         VARCHAR(32)  NOT NULL,
    granted_perms   JSONB        NOT NULL DEFAULT '{}',
    system_config   JSONB,
    created_by      VARCHAR(36)  NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS plugin_versions (
    plugin_id   VARCHAR(128)  NOT NULL,
    version     VARCHAR(64)   NOT NULL,
    digest      VARCHAR(80)   NOT NULL,
    manifest    JSONB         NOT NULL,
    -- Where the package archive is stored (FileService path).
    package_uri VARCHAR(1024) NOT NULL,
    size        BIGINT        NOT NULL DEFAULT 0,
    created_by  VARCHAR(36)   NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, version)
);

COMMENT ON TABLE plugins IS 'Installed (non-builtin) plugins and their desired state; every node converges to it.';
COMMENT ON TABLE plugin_versions IS 'Stored package versions of installed plugins, kept for rollback.';

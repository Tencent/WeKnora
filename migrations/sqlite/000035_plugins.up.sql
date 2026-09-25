-- Installed plugins and their versions (versioned 000116).
CREATE TABLE IF NOT EXISTS plugins (
    id              VARCHAR(128) PRIMARY KEY,
    owner_tenant_id BIGINT,
    source          TEXT         NOT NULL DEFAULT '{}',
    active_version  VARCHAR(64)  NOT NULL,
    desired_state   VARCHAR(16)  NOT NULL DEFAULT 'enabled',
    runtime         VARCHAR(32)  NOT NULL,
    granted_perms   TEXT         NOT NULL DEFAULT '{}',
    system_config   TEXT,
    created_by      VARCHAR(36)  NOT NULL DEFAULT '',
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_versions (
    plugin_id   VARCHAR(128)  NOT NULL,
    version     VARCHAR(64)   NOT NULL,
    digest      VARCHAR(80)   NOT NULL,
    manifest    TEXT          NOT NULL,
    package_uri VARCHAR(1024) NOT NULL,
    size        BIGINT        NOT NULL DEFAULT 0,
    created_by  VARCHAR(36)   NOT NULL DEFAULT '',
    created_at  DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, version)
);

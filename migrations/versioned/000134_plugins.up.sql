-- Marketplaces are shared across tenants: identical archives collapse into one
-- row. Which tenants see a marketplace is recorded in plugin_tenant_bindings.
CREATE TABLE IF NOT EXISTS plugin_marketplaces (
    id            VARCHAR(36)  PRIMARY KEY,
    name          VARCHAR(255) NOT NULL,
    description   TEXT,
    version       VARCHAR(64),
    owner_name    VARCHAR(255),
    owner_email   VARCHAR(255),
    manifest      TEXT,
    bundle_ref    VARCHAR(1024),
    bundle_sha256 VARCHAR(64)  NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_plugin_marketplaces_name_sha256
    ON plugin_marketplaces (name, bundle_sha256) WHERE deleted_at IS NULL;

-- One plugin of a shared marketplace, parsed once at import so that listing,
-- preflight and install never unpack the marketplace archive again. Each
-- entry of skills carries the bundle_ref of its repackaged skill zip.
CREATE TABLE IF NOT EXISTS plugin_packages (
    id               VARCHAR(36)  PRIMARY KEY,
    marketplace_id   VARCHAR(36)  NOT NULL,
    name             VARCHAR(255) NOT NULL,
    position         INTEGER      NOT NULL DEFAULT 0,
    version          VARCHAR(64),
    description      TEXT,
    category         VARCHAR(255),
    keywords         JSONB        NOT NULL DEFAULT '[]',
    requires_sandbox BOOLEAN      NOT NULL DEFAULT FALSE,
    env_declarations JSONB        NOT NULL DEFAULT '[]',
    mcp_servers      JSONB        NOT NULL DEFAULT '{}',
    mcp_warnings     JSONB        NOT NULL DEFAULT '[]',
    skills           JSONB        NOT NULL DEFAULT '[]',
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_plugin_packages_marketplace_name
    ON plugin_packages (marketplace_id, name);

-- Hard-delete ledger of everything a tenant holds through the plugin system:
--   kind = marketplace    -> resource_id is a plugin_marketplaces.id
--   kind = plugin         -> resource_id is a plugin_packages.id; this row is the
--                            install and carries its version/status/sandboxes
--   kind = mcp_service    -> resource_id is an mcp_services.id
--   kind = skill_catalog  -> resource_id is a tenant_skill_catalogs.id
-- mcp_service and skill_catalog rows point at their kind = plugin row through
-- plugin_install_id.
CREATE TABLE IF NOT EXISTS plugin_tenant_bindings (
    id                 VARCHAR(36)  PRIMARY KEY,
    tenant_id          BIGINT       NOT NULL,
    kind               VARCHAR(32)  NOT NULL,
    resource_id        VARCHAR(36)  NOT NULL,
    resource_name      VARCHAR(255) NOT NULL,
    plugin_install_id  VARCHAR(36)  NOT NULL DEFAULT '',
    version            VARCHAR(64)  NOT NULL DEFAULT '',
    status             VARCHAR(32)  NOT NULL DEFAULT '',
    error              TEXT         NOT NULL DEFAULT '',
    sandbox_config_ids JSONB        NOT NULL DEFAULT '[]',
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_plugin_tenant_bindings_tenant
    ON plugin_tenant_bindings (tenant_id);
CREATE INDEX IF NOT EXISTS idx_plugin_tenant_bindings_install
    ON plugin_tenant_bindings (plugin_install_id);
CREATE INDEX IF NOT EXISTS idx_plugin_tenant_bindings_resource
    ON plugin_tenant_bindings (kind, resource_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_plugin_tenant_bindings_tenant_kind_resource
    ON plugin_tenant_bindings (tenant_id, kind, resource_id);
-- A plugin name is held by at most one marketplace per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS uq_plugin_tenant_bindings_plugin_name
    ON plugin_tenant_bindings (tenant_id, resource_name) WHERE kind = 'plugin';
-- A marketplace name is bound at most once per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS uq_plugin_tenant_bindings_marketplace_name
    ON plugin_tenant_bindings (tenant_id, resource_name) WHERE kind = 'marketplace';

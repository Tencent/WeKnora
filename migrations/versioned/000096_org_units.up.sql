-- Migration: 000075_org_units
-- Description: Hierarchical OrgUnit tree under Tenant for ancestor-readable
--              knowledge visibility (province → city → county). Orthogonal to
--              organizations (cross-tenant SharedSpace).

DO $$ BEGIN RAISE NOTICE '[Migration 000075] Creating org_units hierarchy...'; END $$;

-- Administrative units within a single tenant (省/市/县/...).
-- path is a materialized closure string "/id1/id2/id3/" for cheap ancestor
-- queries: ancestors of X are units whose path is a prefix of X.path.
CREATE TABLE IF NOT EXISTS org_units (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  BIGINT NOT NULL,
    parent_id  VARCHAR(36) NOT NULL DEFAULT '',
    name       VARCHAR(255) NOT NULL,
    code       VARCHAR(64) NOT NULL DEFAULT '',
    path       VARCHAR(1024) NOT NULL DEFAULT '',
    depth      INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_units_parent_name
    ON org_units (tenant_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_org_units_tenant_path
    ON org_units (tenant_id, path)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_org_units_tenant_parent
    ON org_units (tenant_id, parent_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_org_units_deleted_at
    ON org_units (deleted_at);

COMMENT ON TABLE org_units IS
    'Tenant-scoped administrative hierarchy (OrgUnit). Not SharedSpace.';
COMMENT ON COLUMN org_units.parent_id IS
    'Empty string means root under the tenant';
COMMENT ON COLUMN org_units.path IS
    'Materialized path including self, e.g. /prov/city/county/';

-- User membership in an OrgUnit (a user may belong to multiple units).
CREATE TABLE IF NOT EXISTS org_unit_members (
    id          VARCHAR(36) PRIMARY KEY,
    org_unit_id VARCHAR(36) NOT NULL REFERENCES org_units(id) ON DELETE CASCADE,
    tenant_id   BIGINT NOT NULL,
    user_id     VARCHAR(36) NOT NULL,
    is_primary  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_unit_members_unit_user
    ON org_unit_members (org_unit_id, user_id);

CREATE INDEX IF NOT EXISTS idx_org_unit_members_tenant_user
    ON org_unit_members (tenant_id, user_id);

CREATE INDEX IF NOT EXISTS idx_org_unit_members_user
    ON org_unit_members (user_id);

COMMENT ON TABLE org_unit_members IS
    'Maps users to OrgUnits within a tenant; is_primary marks default unit';

-- Bind knowledge bases to an OrgUnit. Empty string = unbound (legacy
-- tenant-wide visibility, preserved for backward compatibility).
ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS org_unit_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_org_unit
    ON knowledge_bases (tenant_id, org_unit_id);

COMMENT ON COLUMN knowledge_bases.org_unit_id IS
    'Owning OrgUnit; empty = tenant-wide unbound KB (legacy)';

DO $$ BEGIN RAISE NOTICE '[Migration 000075] org_units hierarchy ready'; END $$;

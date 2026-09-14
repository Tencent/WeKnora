-- SQLite: hierarchical OrgUnit under Tenant (align with versioned 000065).

CREATE TABLE IF NOT EXISTS org_units (
    id         TEXT PRIMARY KEY,
    tenant_id  INTEGER NOT NULL,
    parent_id  TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    code       TEXT NOT NULL DEFAULT '',
    path       TEXT NOT NULL DEFAULT '',
    depth      INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
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

CREATE TABLE IF NOT EXISTS org_unit_members (
    id          TEXT PRIMARY KEY,
    org_unit_id TEXT NOT NULL REFERENCES org_units(id) ON DELETE CASCADE,
    tenant_id   INTEGER NOT NULL,
    user_id     TEXT NOT NULL,
    is_primary  INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_unit_members_unit_user
    ON org_unit_members (org_unit_id, user_id);

CREATE INDEX IF NOT EXISTS idx_org_unit_members_tenant_user
    ON org_unit_members (tenant_id, user_id);

-- knowledge_bases.org_unit_id may already exist on some Lite baselines;
-- ADD COLUMN fails if present, so we tolerate that at apply time via
-- migration runner or a no-op when the column exists.
ALTER TABLE knowledge_bases ADD COLUMN org_unit_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_org_unit
    ON knowledge_bases (tenant_id, org_unit_id);

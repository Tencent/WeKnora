-- SQLite: platform root OrgUnit → workspace Tenant mapping
-- (align with versioned 000080).

CREATE TABLE IF NOT EXISTS org_unit_workspaces (
    root_org_unit_id TEXT PRIMARY KEY,
    tenant_id        INTEGER NOT NULL UNIQUE,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_unit_workspaces_tenant
    ON org_unit_workspaces (tenant_id);

-- SQLite: invitation org_unit binding (align with versioned 000066).

ALTER TABLE tenant_invitations ADD COLUMN org_unit_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tenant_invitations_org_unit
    ON tenant_invitations (tenant_id, org_unit_id);

-- Migration: 000081_org_unit_single_membership
-- One OrgUnit membership per user per tenant.

DO $$ BEGIN RAISE NOTICE
  '[Migration 000081] Enforcing single org_unit membership...';
END $$;

DELETE FROM org_unit_members a
USING org_unit_members b
WHERE a.tenant_id = b.tenant_id
  AND a.user_id = b.user_id
  AND a.id <> b.id
  AND (
    (b.is_primary = TRUE AND a.is_primary = FALSE)
    OR (
      a.is_primary = b.is_primary
      AND (
        a.created_at > b.created_at
        OR (a.created_at = b.created_at AND a.id > b.id)
      )
    )
  );

DROP INDEX IF EXISTS idx_org_unit_members_tenant_user;

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_unit_members_tenant_user_unique
    ON org_unit_members (tenant_id, user_id);

COMMENT ON TABLE org_unit_members IS
    'Maps users to exactly one OrgUnit per tenant; is_primary is legacy';

DO $$ BEGIN RAISE NOTICE '[Migration 000081] done'; END $$;

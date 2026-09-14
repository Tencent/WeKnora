-- Migration: 000009_org_unit_single_membership
-- One OrgUnit membership per user per tenant.
-- Prefer is_primary=1, else oldest created_at, else smallest id.
-- Correlated subquery (no window functions) for SQLite compatibility.

DELETE FROM org_unit_members
WHERE id NOT IN (
  SELECT keep_id FROM (
    SELECT (
      SELECT preferred.id
      FROM org_unit_members AS preferred
      WHERE preferred.tenant_id = members.tenant_id
        AND preferred.user_id = members.user_id
      ORDER BY preferred.is_primary DESC,
               preferred.created_at ASC,
               preferred.id ASC
      LIMIT 1
    ) AS keep_id
    FROM org_unit_members AS members
    GROUP BY members.tenant_id, members.user_id
  )
);

DROP INDEX IF EXISTS idx_org_unit_members_tenant_user;

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_unit_members_tenant_user_unique
    ON org_unit_members (tenant_id, user_id);

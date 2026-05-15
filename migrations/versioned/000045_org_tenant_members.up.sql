-- Migration: 000045_org_tenant_members
--
-- Plan 3 of issue #1303: lift "organization membership" from per-user to
-- per-tenant. Today `organization_members` is keyed on (org_id, user_id) —
-- if Alice (tenant T) joins Org X, her tenant-mate Bob has no visibility
-- into anything shared into Org X. That breaks the company-level
-- collaboration mental model ("our company is in this org") and creates
-- inconsistency with `tenant_disabled_shared_agents`, which is already
-- tenant-scoped.
--
-- The new table `organization_tenant_members` is keyed on (org_id,
-- tenant_id). One row says "tenant T participates in Org O at role R,
-- represented by user U". `representative_user_id` is purely for UI/audit
-- ("who from this tenant brought us here"); permission checks use only
-- (org_id, tenant_id, role).
--
-- Backfill policy: collapse all `organization_members` rows for the same
-- (org, tenant) into a single new row. Role = the highest role observed
-- across the group (admin > editor > viewer). representative_user_id =
-- the earliest joiner. `RAISE NOTICE` flags any (org, tenant) that had
-- conflicting roles so operators can audit the resolved choice.
--
-- The old `organization_members` table is RENAMEd to
-- `organization_members_pre_plan3` so a `down.sql` rollback can restore
-- it. A future destructive migration (gated on
-- `weknora.allow_destructive_migration`) will DROP it once the new model
-- is settled.

DO $$ BEGIN RAISE NOTICE '[Migration 000045] Starting Plan 3: org members → org tenant members'; END $$;

-- 1. New table.
DO $$ BEGIN RAISE NOTICE '[Migration 000045] Creating table: organization_tenant_members'; END $$;
CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id                      VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id         VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    tenant_id               INTEGER NOT NULL,
    role                    VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id  VARCHAR(36) NOT NULL DEFAULT '',
    joined_at               TIMESTAMP WITH TIME ZONE,
    created_at              TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_tenant_members_unique
    ON organization_tenant_members (organization_id, tenant_id);

CREATE INDEX IF NOT EXISTS idx_org_tenant_members_by_tenant
    ON organization_tenant_members (tenant_id);

CREATE INDEX IF NOT EXISTS idx_org_tenant_members_role
    ON organization_tenant_members (organization_id, role);

COMMENT ON TABLE organization_tenant_members IS 'Plan 3: Tenants (not users) are organization members.';
COMMENT ON COLUMN organization_tenant_members.role IS 'Tenant role inside the org: admin | editor | viewer.';
COMMENT ON COLUMN organization_tenant_members.representative_user_id IS 'Display-only: the user who first brought this tenant into the org.';

-- 2. Surface conflicting roles before backfilling so operators see them
--    in the migration log. The collapse strategy is "max role wins"
--    (admin > editor > viewer); this is a permission *promotion* for
--    any user who was at a lower role inside their tenant, so we shout
--    loudly when it happens.
DO $$
DECLARE
    rec RECORD;
BEGIN
    FOR rec IN
        SELECT organization_id, tenant_id,
               COUNT(*)                  AS user_count,
               ARRAY_AGG(DISTINCT role)  AS observed_roles,
               CASE
                   WHEN BOOL_OR(role = 'admin')  THEN 'admin'
                   WHEN BOOL_OR(role = 'editor') THEN 'editor'
                   ELSE 'viewer'
               END AS resolved_role
        FROM organization_members
        GROUP BY organization_id, tenant_id
    LOOP
        IF rec.user_count > 1 AND ARRAY_LENGTH(rec.observed_roles, 1) > 1 THEN
            RAISE NOTICE '[Migration 000045] dedup org=% tenant=% chose role=% from observed=%, user_count=%',
                rec.organization_id, rec.tenant_id, rec.resolved_role, rec.observed_roles, rec.user_count;
        END IF;
    END LOOP;
END $$;

-- 3. Backfill the new table from the old one.
DO $$ BEGIN RAISE NOTICE '[Migration 000045] Backfilling organization_tenant_members from organization_members'; END $$;
INSERT INTO organization_tenant_members (
    organization_id, tenant_id, role, representative_user_id, joined_at, created_at, updated_at
)
SELECT
    om.organization_id,
    om.tenant_id,
    CASE
        WHEN BOOL_OR(om.role = 'admin')  THEN 'admin'
        WHEN BOOL_OR(om.role = 'editor') THEN 'editor'
        ELSE 'viewer'
    END AS role,
    -- Representative: earliest joiner in the group, fallback to lowest user_id
    -- so the choice is deterministic across re-runs.
    (SELECT om2.user_id
       FROM organization_members om2
      WHERE om2.organization_id = om.organization_id
        AND om2.tenant_id       = om.tenant_id
      ORDER BY om2.created_at ASC, om2.user_id ASC
      LIMIT 1) AS representative_user_id,
    MIN(om.created_at) AS joined_at,
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
FROM organization_members om
GROUP BY om.organization_id, om.tenant_id
ON CONFLICT DO NOTHING;

-- 4. Park the old table so `down.sql` can restore it. We do NOT drop it
--    here so a botched rollout can be reversed without re-creating data.
--    A follow-up destructive migration removes it in a later release.
DO $$ BEGIN RAISE NOTICE '[Migration 000045] Renaming organization_members → organization_members_pre_plan3'; END $$;
ALTER TABLE organization_members RENAME TO organization_members_pre_plan3;

-- The unique-index name must move with the table; renaming the table does
-- not rename the indices in older PG versions, so do it explicitly.
ALTER INDEX IF EXISTS idx_org_members_org_user      RENAME TO idx_org_members_org_user_pre_plan3;
ALTER INDEX IF EXISTS idx_org_members_user_id       RENAME TO idx_org_members_user_id_pre_plan3;
ALTER INDEX IF EXISTS idx_org_members_tenant_id     RENAME TO idx_org_members_tenant_id_pre_plan3;
ALTER INDEX IF EXISTS idx_org_members_role          RENAME TO idx_org_members_role_pre_plan3;

DO $$ BEGIN RAISE NOTICE '[Migration 000045] Plan 3 setup ready'; END $$;

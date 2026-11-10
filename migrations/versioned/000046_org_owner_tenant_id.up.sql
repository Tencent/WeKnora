-- Migration: 000046_org_owner_tenant_id
--
-- Plan 3 follow-up to #1303: pin the org's "owning tenant" in the
-- organizations table itself instead of recomputing it on every
-- permission check from the owner user's *current* tenant. The old
-- `isOwnerTenant(org, t) == (owner.user.TenantID == t)` rule breaks
-- silently when the owner user moves to a different tenant: the
-- formerly-protected owning tenant becomes removable, and the new
-- tenant the owner moved to (which probably isn't even in OTM) starts
-- being treated as untouchable. Storing owner_tenant_id at create time
-- and never changing it post-hoc gives us a single source of truth.
--
-- Backfill strategy:
--   1. derive from the owner user's current tenant (works for the
--      vast majority of orgs);
--   2. for orgs whose owner user is gone, fall back to the earliest
--      Admin tenant in OTM — deterministic across re-runs;
--   3. anything still NULL means the org is genuinely orphaned (no
--      owner user, no admin tenant) — abort the migration with a loud
--      RAISE EXCEPTION so the operator deals with it manually.
--      We refuse to silently ship an unconstrained NOT NULL since it
--      would mean some org rows are unreachable.

DO $$ BEGIN RAISE NOTICE '[Migration 000046] Adding organizations.owner_tenant_id'; END $$;

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS owner_tenant_id BIGINT;

-- Pass 1: owner user still exists.
UPDATE organizations o
   SET owner_tenant_id = u.tenant_id
  FROM users u
 WHERE o.owner_id        = u.id
   AND o.owner_tenant_id IS NULL;

-- Pass 2: orphan owner — pick the earliest Admin tenant in OTM.
UPDATE organizations o
   SET owner_tenant_id = sub.tenant_id
  FROM (
        SELECT DISTINCT ON (otm.organization_id)
               otm.organization_id,
               otm.tenant_id
          FROM organization_tenant_members otm
         WHERE otm.role = 'admin'
         ORDER BY otm.organization_id, otm.created_at ASC, otm.tenant_id ASC
       ) sub
 WHERE sub.organization_id = o.id
   AND o.owner_tenant_id   IS NULL;

-- Surface anything still unresolved. We deliberately fail the migration
-- here instead of guessing — making the column NOT NULL with a bogus
-- backfill would corrupt permission checks for those orgs forever.
DO $$
DECLARE
    orphan_count INT;
BEGIN
    SELECT COUNT(*) INTO orphan_count
      FROM organizations
     WHERE owner_tenant_id IS NULL
       AND deleted_at      IS NULL;
    IF orphan_count > 0 THEN
        RAISE EXCEPTION
            '[Migration 000046] % orphan organization(s) have no resolvable owner_tenant_id (owner user missing AND no admin tenant in OTM). Either soft-delete them or backfill manually before retrying. Inspect with: SELECT id, name, owner_id FROM organizations WHERE owner_tenant_id IS NULL AND deleted_at IS NULL;',
            orphan_count;
    END IF;
END $$;

-- Lock in the invariant.
ALTER TABLE organizations
    ALTER COLUMN owner_tenant_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_organizations_owner_tenant
    ON organizations (owner_tenant_id);

COMMENT ON COLUMN organizations.owner_tenant_id IS
    'Plan 3 (#1303): owning tenant; cannot be removed/downgraded from OTM.';

DO $$ BEGIN RAISE NOTICE '[Migration 000046] organizations.owner_tenant_id ready'; END $$;

-- Migration: 000043_tenant_rbac
-- Introduces tenant-level RBAC (issue #1303):
--   1. tenant_members table holds the (user, tenant) role assignments that replace
--      the coarse "User.TenantID only" model. A user may now have rows in multiple
--      tenants with potentially different roles.
--   2. knowledge_bases.creator_id records who created a KB so Contributors can edit
--      their own without full tenant-wide edit rights. custom_agents.created_by
--      already exists and is reused as-is.
--   3. custom_agents.runnable_by_viewer controls whether TenantRoleViewer users
--      may start sessions against an agent.
--
-- Backfill policy (existing data):
--   - In each tenant, the earliest-created active user becomes 'owner'; any other
--     users become 'contributor'. This preserves today's "anyone can create KBs"
--     behaviour for non-first users while giving each tenant exactly one owner.
--   - knowledge_bases.creator_id is set to that tenant's owner, so Admins/Owners
--     keep full control and Contributors do not unexpectedly inherit ownership of
--     pre-existing resources.
--   - API-key-only tenants (tenants with no human users) get no membership rows;
--     the auth middleware auto-promotes the first human authenticating into such
--     a tenant to Owner.
DO $$ BEGIN RAISE NOTICE '[Migration 000043] Starting tenant RBAC setup...'; END $$;

-- 1. tenant_members table
DO $$ BEGIN RAISE NOTICE '[Migration 000043] Creating table: tenant_members'; END $$;
CREATE TABLE IF NOT EXISTS tenant_members (
    id          BIGSERIAL PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    tenant_id   INTEGER NOT NULL,
    role        VARCHAR(20) NOT NULL DEFAULT 'contributor',
    status      VARCHAR(20) NOT NULL DEFAULT 'active',
    invited_by  VARCHAR(36),
    joined_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  TIMESTAMP WITH TIME ZONE
);

-- Partial unique index: at most one non-deleted membership per (user, tenant).
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant_role
    ON tenant_members(tenant_id, role)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tenant_members_user
    ON tenant_members(user_id)
    WHERE deleted_at IS NULL;

-- 2. Backfill one membership row per existing active user.
--    Earliest-created active user per tenant => owner; others => contributor.
DO $$ BEGIN RAISE NOTICE '[Migration 000043] Backfilling tenant_members rows from users'; END $$;
INSERT INTO tenant_members (user_id, tenant_id, role, status, joined_at, created_at, updated_at)
SELECT u.id,
       u.tenant_id,
       CASE
           WHEN u.id = (
               SELECT u2.id FROM users u2
                WHERE u2.tenant_id = u.tenant_id
                  AND u2.is_active = TRUE
                  AND u2.deleted_at IS NULL
                ORDER BY u2.created_at ASC, u2.id ASC
                LIMIT 1
           ) THEN 'owner'
           ELSE 'contributor'
       END AS role,
       'active',
       u.created_at,
       CURRENT_TIMESTAMP,
       CURRENT_TIMESTAMP
  FROM users u
 WHERE u.deleted_at IS NULL
   AND u.is_active = TRUE
   AND u.tenant_id IS NOT NULL
   AND u.tenant_id <> 0
ON CONFLICT DO NOTHING;

-- 3. knowledge_bases.creator_id
DO $$ BEGIN RAISE NOTICE '[Migration 000043] Adding creator_id to knowledge_bases'; END $$;
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS creator_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_creator
    ON knowledge_bases(tenant_id, creator_id);

-- Backfill KB creator to the tenant's owner. Rows in tenants without any human
-- users (API-key-only) keep creator_id NULL; the application layer treats NULL
-- creator as "tenant-owned" and requires Admin+ to mutate.
UPDATE knowledge_bases kb
   SET creator_id = (
       SELECT tm.user_id
         FROM tenant_members tm
        WHERE tm.tenant_id = kb.tenant_id
          AND tm.role = 'owner'
          AND tm.status = 'active'
          AND tm.deleted_at IS NULL
        ORDER BY tm.joined_at ASC, tm.id ASC
        LIMIT 1
   )
 WHERE kb.creator_id IS NULL;

-- 4. custom_agents.runnable_by_viewer
DO $$ BEGIN RAISE NOTICE '[Migration 000043] Adding runnable_by_viewer to custom_agents'; END $$;
ALTER TABLE custom_agents
    ADD COLUMN IF NOT EXISTS runnable_by_viewer BOOLEAN NOT NULL DEFAULT TRUE;

DO $$ BEGIN RAISE NOTICE '[Migration 000043] tenant RBAC setup ready'; END $$;

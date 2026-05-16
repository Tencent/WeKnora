-- Migration: 000047_user_resource_favorites
-- Per-(user, tenant) starred resources (knowledge bases and custom agents).
--
-- Why (user_id, tenant_id) instead of (user_id) alone:
--   A user can be a member of multiple tenants with very different
--   resource sets; a KB called "design specs" in tenant A is not the
--   same thing as "design specs" in tenant B and the user almost
--   certainly wants to star them independently. Scoping by tenant also
--   simplifies cleanup: drop a tenant -> drop its favorites cleanly via
--   the (tenant_id) index without orphaning rows.
--
-- Why a single table instead of one per resource_type:
--   Adding "favorite a mcp tool" or "favorite a wiki page" later is a
--   new constant string, not a new schema migration. The PK includes
--   resource_type so different types with colliding ids can coexist.
--
-- No FK to knowledge_bases / custom_agents:
--   Favorites survive across share revocations / re-grants and across
--   the eventual soft-delete -> hard-delete window. Hydrating with a
--   LEFT JOIN at read time is cheap (<= ~30 rows per user) and lets us
--   silently drop entries for resources the user can no longer see.
DO $$ BEGIN RAISE NOTICE '[Migration 000047] Creating table: user_resource_favorites'; END $$;

CREATE TABLE IF NOT EXISTS user_resource_favorites (
    user_id        VARCHAR(36) NOT NULL,
    tenant_id      BIGINT      NOT NULL,
    resource_type  VARCHAR(16) NOT NULL,   -- 'kb' | 'agent' (extensible)
    resource_id    VARCHAR(64) NOT NULL,
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, tenant_id, resource_type, resource_id)
);

-- Primary read path: "list this user's favorites of this type in this
-- tenant, newest first". The PK already covers (user_id, tenant_id, …)
-- equality, but we want created_at DESC ordering without a sort step.
CREATE INDEX IF NOT EXISTS idx_user_resource_favorites_user_tenant_type_created_at
    ON user_resource_favorites (user_id, tenant_id, resource_type, created_at DESC);

-- Cleanup path when a tenant is deleted: bulk DELETE WHERE tenant_id = ?
CREATE INDEX IF NOT EXISTS idx_user_resource_favorites_tenant_id
    ON user_resource_favorites (tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000047] user_resource_favorites table ready'; END $$;

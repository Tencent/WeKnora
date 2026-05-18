-- Migration: 000050_user_kb_pins
-- Per-(user, tenant, kb) pin state for the knowledge-base list.
--
-- Previously KB pinning lived on the `knowledge_bases.is_pinned` /
-- `pinned_at` columns and was therefore tenant-wide: one admin's pin
-- reordered the list for every member of the tenant, and the RBAC guard
-- on PUT /knowledge-bases/:id/pin (OwnedKBOrAdmin) hid the affordance
-- entirely from Viewer / Contributor users who could see the KB but not
-- edit it. Both behaviours were confusing and inconsistent with the
-- session-pin model (which has always been per-user).
--
-- This migration introduces a dedicated mapping table and backfills
-- existing tenant-wide pins onto the KB's CreatorID, so the admin who
-- pinned a KB still sees it pinned after upgrade while other tenant
-- members get a clean per-user starting point. The legacy columns on
-- knowledge_bases are left in place for one release to keep rollback
-- safe; new code stops writing them and reads pin state from this
-- table instead.
DO $$ BEGIN RAISE NOTICE '[Migration 000050] Creating table: user_kb_pins'; END $$;

CREATE TABLE IF NOT EXISTS user_kb_pins (
    tenant_id  BIGINT      NOT NULL,
    user_id    VARCHAR(36) NOT NULL,
    kb_id      VARCHAR(36) NOT NULL,
    pinned_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, kb_id)
);

-- Primary read path: "give me this user's pinned KB ids in this tenant,
-- newest pin first". The PK already covers equality on (tenant_id,
-- user_id, kb_id); the secondary index lets us avoid an in-memory sort
-- when stamping the list response.
CREATE INDEX IF NOT EXISTS idx_user_kb_pins_user_tenant_pinned_at
    ON user_kb_pins (tenant_id, user_id, pinned_at DESC);

-- Backfill: every KB that is currently pinned at the tenant level gets
-- carried over as a personal pin for its creator. KBs whose creator_id
-- is empty / NULL (legacy rows that predate the RBAC migration) are
-- intentionally skipped — we have no user to attribute the pin to, and
-- silently dropping these is preferable to attributing them to a tenant
-- owner who may not actually want the KB pinned in their view.
INSERT INTO user_kb_pins (tenant_id, user_id, kb_id, pinned_at)
SELECT kb.tenant_id,
       kb.creator_id,
       kb.id,
       COALESCE(kb.pinned_at, CURRENT_TIMESTAMP)
  FROM knowledge_bases kb
 WHERE kb.is_pinned = TRUE
   AND kb.creator_id IS NOT NULL
   AND kb.creator_id <> ''
ON CONFLICT (tenant_id, user_id, kb_id) DO NOTHING;

DO $$ BEGIN RAISE NOTICE '[Migration 000050] user_kb_pins table ready'; END $$;

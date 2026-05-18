-- Migration: 000051_custom_agents_creator_backfill
-- Backfill custom_agents.created_by for legacy non-builtin rows.
--
-- Background:
--   * `custom_agents.created_by` was introduced together with the table in
--     000006 but the seed INSERT statements for the two built-in agents
--     (builtin-quick-answer / builtin-smart-reasoning) intentionally omit
--     this column — built-ins are tenant-shared system rows, so they
--     legitimately carry NULL / empty creator_id. `AgentCreatorLookup`
--     short-circuits on IsBuiltin=true and treats this as expected.
--   * The RBAC migration 000043 backfilled `knowledge_bases.creator_id`
--     to the tenant owner so Contributors keep "owner" access to legacy
--     KBs they actually created, but it forgot the symmetrical update on
--     `custom_agents`. As a result any non-builtin agent created before
--     the per-row creator tracking landed (or written by tooling that
--     skipped UserIDFromContext) has empty `created_by` and falls into
--     the "tenant-owned, Admin+ only" bucket — the historical creator
--     can no longer self-edit, and the `?creator=mine|others` list
--     filter silently drops the row.
--
-- This migration applies the same owner-of-tenant fallback as
-- knowledge_bases, scoped strictly to non-builtin rows so the
-- intentional empty creator on built-in agents is preserved.
DO $$ BEGIN RAISE NOTICE '[Migration 000051] Backfilling custom_agents.created_by'; END $$;

UPDATE custom_agents ca
   SET created_by = (
       SELECT tm.user_id
         FROM tenant_members tm
        WHERE tm.tenant_id = ca.tenant_id
          AND tm.role = 'owner'
          AND tm.status = 'active'
          AND tm.deleted_at IS NULL
        ORDER BY tm.joined_at ASC, tm.id ASC
        LIMIT 1
   )
 WHERE ca.is_builtin = FALSE
   AND (ca.created_by IS NULL OR ca.created_by = '')
   AND EXISTS (
       SELECT 1
         FROM tenant_members tm
        WHERE tm.tenant_id = ca.tenant_id
          AND tm.role = 'owner'
          AND tm.status = 'active'
          AND tm.deleted_at IS NULL
   );

DO $$ BEGIN RAISE NOTICE '[Migration 000051] custom_agents.created_by backfill complete'; END $$;

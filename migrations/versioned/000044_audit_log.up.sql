-- Migration: 000044_audit_log
-- Adds a generic per-tenant audit log table (issue #1303 PR 6).
--
-- Scope:
--   - PR 6 wires the table to RBAC events (member add/remove/role-change/
--     leave) and middleware-level enforcement denials.
--   - The schema is intentionally generic (action, target_type, target_id,
--     details JSONB) so future PRs (KB ops, agent ops, datasource sync)
--     can plug in new action constants without another migration.
--
-- Indexes:
--   - (tenant_id, id DESC) for the cursor-paginated feed query
--     ("show this tenant's audit log newest-first").
--   - (actor_user_id) for "what did Alice do" lookups.
--   - (tenant_id, action) for action-class filtering and powering the
--     1-minute sliding-window dedup that LogDenied uses to keep a
--     probing client from filling the table.
DO $$ BEGIN RAISE NOTICE '[Migration 000044] Creating table: audit_logs'; END $$;

CREATE TABLE IF NOT EXISTS audit_logs (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    actor_user_id   VARCHAR(36) NOT NULL DEFAULT '',
    actor_role      VARCHAR(32) NOT NULL DEFAULT '',
    action          VARCHAR(64) NOT NULL,
    target_type     VARCHAR(32) NOT NULL DEFAULT '',
    target_id       VARCHAR(64) NOT NULL DEFAULT '',
    target_user_id  VARCHAR(36) NOT NULL DEFAULT '',
    request_path    VARCHAR(512) NOT NULL DEFAULT '',
    request_method  VARCHAR(16)  NOT NULL DEFAULT '',
    outcome         VARCHAR(16)  NOT NULL DEFAULT 'success',
    details         JSONB        NOT NULL DEFAULT '{}'::JSONB,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Primary list query: newest-first cursor pagination per tenant. Using id
-- (BIGSERIAL, monotonic) as the cursor sidesteps the duplicate-timestamp
-- tie-breaking that a created_at cursor would require — same approach as
-- wiki_log_entries (migration 000040).
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_id_desc
    ON audit_logs (tenant_id, id DESC);

-- Filter by actor (e.g. "what did Alice do"). Useful for incident
-- response and for user-scoped self-audit screens (out of v1 scope but
-- the index is cheap and the column is already there).
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor
    ON audit_logs (actor_user_id);

-- Powers two queries: (a) the audit-log feed filtered by action class,
-- and (b) the LogDenied dedup `count rows where (tenant_id, action) and
-- created_at >= since`. Without this index the dedup count would scan
-- the entire table on every denied request.
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_action
    ON audit_logs (tenant_id, action);

-- Powers the daily retention sweep `DELETE FROM audit_logs WHERE
-- created_at < cutoff`. Without it the sweep would Seq Scan the whole
-- table on every run, which on a 90-day-retained tenant with bursty
-- RBAC traffic is enough to blow past the runner's 30s timeout and
-- never converge. Indexing created_at also keeps the per-day DELETE
-- bounded to roughly one day's worth of rows once the table reaches
-- steady state.
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at
    ON audit_logs (created_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000044] audit_logs table ready'; END $$;

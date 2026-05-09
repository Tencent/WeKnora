-- Migration: 000041_task_queue_and_wiki_indexes
-- Description: Generic task pending queue + dead-letter table, and three
--              additional GIN indexes on wiki_pages to back the optimized
--              ingest pipeline (4w-document-scale).
--
-- Schemas introduced:
--   1. task_pending_ops    — durable replacement for the Redis-list-backed
--                            wiki:pending:<kbID> queue. Designed generically
--                            (task_type + scope + scope_id) so future
--                            "debounced batch" task types can reuse it
--                            without another migration.
--   2. task_dead_letters   — generic archived-task record. Written by both
--                            (a) the asynq middleware when a task exhausts
--                            its retry budget, and (b) the wiki ingest
--                            service when a per-document op exceeds its
--                            in-batch retry quota.
--   3. wiki_pages indexes  — GIN on source_refs (containment + text), and
--                            trigram on lower(title) for the new pg_trgm
--                            dedup pre-filter.

DO $$ BEGIN RAISE NOTICE '[Migration 000041] Applying task queue + wiki indexes schema'; END $$;

-- ---------------------------------------------------------------------------
-- 1) task_pending_ops
--
-- Generic replacement for ad-hoc Redis-list pending queues. Each row is one
-- pending operation scoped to a (task_type, scope, scope_id) tuple. The
-- consumer (e.g. wiki ingest batch handler) uses PeekBatch to pull the head
-- of the list, processes the ops, then DeleteByIDs the consumed rows.
--
-- claimed_at is intentionally added but unused in this revision: the wiki
-- ingest pipeline guarantees per-KB serialization via its own active-batch
-- lock (Redis SetNX wiki:active:<kbID>), so PeekBatch doesn't need
-- SELECT ... FOR UPDATE. Future task types that don't carry an external
-- lock can flip claimed_at = NOW() inside a row-level lock and rely on the
-- column to detect orphaned claims after a worker crash.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    op          VARCHAR(32) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT '',
    payload     JSONB NOT NULL DEFAULT '{}'::JSONB,
    fail_count  INT NOT NULL DEFAULT 0,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at  TIMESTAMPTZ
);

COMMENT ON TABLE task_pending_ops IS 'Generic durable pending-op queue keyed by (task_type, scope, scope_id). Replaces ad-hoc Redis-list queues that were vulnerable to TTL eviction.';
COMMENT ON COLUMN task_pending_ops.task_type IS 'Free-form task identifier, e.g. "wiki:ingest" — should match an asynq task type when applicable.';
COMMENT ON COLUMN task_pending_ops.scope IS 'Logical scope, e.g. "knowledge_base" / "knowledge" / "tenant". Read together with scope_id.';
COMMENT ON COLUMN task_pending_ops.dedup_key IS 'Optional service-defined key used by the consumer to de-duplicate equivalent ops within a single batch peek. Empty means no de-dup.';
COMMENT ON COLUMN task_pending_ops.fail_count IS 'In-batch retry counter: the consumer increments it via IncrFailCount and dead-letters once it exceeds a service-defined cap.';
COMMENT ON COLUMN task_pending_ops.claimed_at IS 'Reserved for future locking workflows; consumers in this revision rely on external mutual exclusion and ignore the column.';

-- Cover the PeekBatch query: the consumer scans rows for one
-- (task_type, scope, scope_id) tuple ordered by id ASC.
CREATE INDEX IF NOT EXISTS idx_task_pending_ops_scope
    ON task_pending_ops (task_type, scope, scope_id, id);

CREATE INDEX IF NOT EXISTS idx_task_pending_ops_tenant
    ON task_pending_ops (tenant_id);

-- ---------------------------------------------------------------------------
-- 2) task_dead_letters
--
-- Permanent archive for tasks that exhausted retries. Written from two
-- distinct paths:
--   (a) The asynq dead-letter middleware (internal/middleware/asynq_dead_letter.go)
--       inserts a row whenever a task's retry count equals MaxRetry on the
--       way out — covers every asynq task type uniformly.
--   (b) The wiki ingest service inserts a row directly when a per-document
--       op exceeds wikiMaxFailRetries inside a batch — these never escalate
--       to an asynq retry because they're handled inline.
--
-- Operations queries it by scope (e.g. all dead letters for a KB) or by
-- task_type (e.g. all summary:generation failures in the last 24h). No
-- TTL — rows are kept for postmortem until manually pruned. failed_at DESC
-- so newest-first scans are cheap.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS task_dead_letters (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    related_id  VARCHAR(64) NOT NULL DEFAULT '',
    payload     JSONB NOT NULL,
    last_error  TEXT NOT NULL DEFAULT '',
    fail_count  INT NOT NULL,
    failed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE task_dead_letters IS 'Permanent archive of tasks that exhausted retries. Written by the asynq dead-letter middleware and by service-level retry handlers (e.g. wiki ingest per-doc retries).';
COMMENT ON COLUMN task_dead_letters.related_id IS 'Optional secondary identifier. Wiki ingest puts knowledge_id here so retract/ingest dead letters cluster around the source document.';
COMMENT ON COLUMN task_dead_letters.payload IS 'Raw task payload (asynq.Task.Payload) at the time of failure. Allows manual requeue via SQL + asynq.Client.Enqueue.';
COMMENT ON COLUMN task_dead_letters.last_error IS 'String form of the error that caused the final retry to fail. Long stack traces are kept verbatim.';

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_scope
    ON task_dead_letters (scope, scope_id, failed_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_tenant
    ON task_dead_letters (tenant_id, failed_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_task_type
    ON task_dead_letters (task_type, failed_at DESC);

-- ---------------------------------------------------------------------------
-- 3) wiki_pages: source_refs / title trigram indexes
--
-- (a) source_refs containment GIN — covers `source_refs @> ?::jsonb`. Used
--     by ListBySourceRef when looking up pages that cite a given knowledge
--     id (delete flow, retract reconciliation, getExistingPageSlugsForKnowledge).
--
-- (b) source_refs text-fulltext GIN — fallback for the legacy "kid|title"
--     ref form, which the runtime LIKE pattern needs. Same shape as the
--     existing fulltext index in 000037 (line 54-55).
--
-- (c) title trigram GIN — backs the new pg_trgm-based dedup pre-filter
--     (selectDedupCandidatePages). pg_trgm extension is already loaded by
--     migration 000002.
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_wiki_pages_source_refs
    ON wiki_pages USING GIN (source_refs jsonb_path_ops);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_source_refs_text
    ON wiki_pages USING GIN (to_tsvector('simple', source_refs::text));

CREATE INDEX IF NOT EXISTS idx_wiki_pages_title_trgm
    ON wiki_pages USING GIN (lower(title) gin_trgm_ops);

DO $$ BEGIN RAISE NOTICE '[Migration 000041] task queue + wiki indexes schema applied successfully'; END $$;

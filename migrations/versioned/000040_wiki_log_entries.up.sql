-- Migration: 000040_wiki_log_entries
-- Description: Dedicated append-only event table for wiki operation log entries.
--              Replaces the "single giant TEXT column on slug='log' wiki_pages
--              row" model that caused O(n^2) write amplification as KBs grew:
--              every ingest/retract op previously did GetLog + UpdatePage
--              rewriting the entire (potentially multi-MB) TEXT column.
--              Here each event is one INSERT; reads paginate by (kb_id, id DESC).

DO $$ BEGIN RAISE NOTICE '[Migration 000040] Applying wiki_log_entries schema'; END $$;

CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action            VARCHAR(32) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL DEFAULT '',
    doc_title         TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    pages_affected    JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Primary list query: cursor-paginated feed per KB, newest first.
-- Using id (BIGSERIAL, monotonic) as the cursor sidesteps duplicate-timestamp
-- tie-breaking that a created_at cursor would require.
CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_kb_id_desc
    ON wiki_log_entries (knowledge_base_id, id DESC);

CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_tenant_id
    ON wiki_log_entries (tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000040] wiki_log_entries schema applied successfully'; END $$;

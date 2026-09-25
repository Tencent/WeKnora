-- Migration: 000077_remove_wiki_log (rollback)
-- Description: Restore the former Wiki operation log schema. Previously
--              deleted log rows cannot be reconstructed.

DO $$ BEGIN RAISE NOTICE '[Migration 000077 DOWN] Restoring wiki_log_entries schema'; END $$;

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

CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_kb_id_desc
    ON wiki_log_entries (knowledge_base_id, id DESC);

CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_tenant_id
    ON wiki_log_entries (tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000077 DOWN] wiki_log_entries schema restored'; END $$;

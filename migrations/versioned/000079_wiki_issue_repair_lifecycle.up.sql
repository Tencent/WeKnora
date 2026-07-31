-- Migration: 000079_wiki_issue_repair_lifecycle
-- Unifies lint findings, durable repair attempts, and page-version evidence.

ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS page_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS severity VARCHAR(20) NOT NULL DEFAULT 'warning';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS source VARCHAR(20) NOT NULL DEFAULT 'agent';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS fingerprint VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS evidence JSONB NOT NULL DEFAULT '{}'::JSONB;
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS repair_mode VARCHAR(20) NOT NULL DEFAULT 'agent';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS detected_page_version INT NOT NULL DEFAULT 0;
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS last_seen_run_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS occurrence_count INT NOT NULL DEFAULT 1;
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS active_attempt_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS resolution_action VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS resolution_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE wiki_page_issues ADD COLUMN IF NOT EXISTS resolved_page_version INT NOT NULL DEFAULT 0;

ALTER TABLE wiki_page_issues ALTER COLUMN status SET DEFAULT 'open';

CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_page_id ON wiki_page_issues(page_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_source_status ON wiki_page_issues(knowledge_base_id, source, status);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_fingerprint ON wiki_page_issues(knowledge_base_id, fingerprint);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_issue_fingerprint ON wiki_page_issues(knowledge_base_id, fingerprint);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_active_attempt ON wiki_page_issues(active_attempt_id);

CREATE TABLE IF NOT EXISTS wiki_lint_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    status VARCHAR(20) NOT NULL,
    rule_version VARCHAR(32) NOT NULL DEFAULT '',
    progress INT NOT NULL DEFAULT 0,
    finding_count INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wiki_lint_runs_kb_created ON wiki_lint_runs(knowledge_base_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_lint_runs_one_active
    ON wiki_lint_runs(knowledge_base_id) WHERE status IN ('queued', 'running');

CREATE TABLE IF NOT EXISTS wiki_repair_attempts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    issue_id VARCHAR(36) NOT NULL,
    page_id VARCHAR(36) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    mode VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL,
    before_version INT NOT NULL DEFAULT 0,
    after_version INT NOT NULL DEFAULT 0,
    action VARCHAR(32) NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wiki_repair_attempts_issue ON wiki_repair_attempts(issue_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wiki_repair_attempts_active ON wiki_repair_attempts(knowledge_base_id, status);
CREATE INDEX IF NOT EXISTS idx_wiki_repair_attempts_session ON wiki_repair_attempts(session_id);

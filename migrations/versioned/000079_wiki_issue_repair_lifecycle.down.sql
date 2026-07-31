DROP TABLE IF EXISTS wiki_repair_attempts;
DROP INDEX IF EXISTS idx_wiki_lint_runs_one_active;
DROP TABLE IF EXISTS wiki_lint_runs;

DROP INDEX IF EXISTS idx_wiki_page_issues_active_attempt;
DROP INDEX IF EXISTS idx_wiki_page_issues_fingerprint;
DROP INDEX IF EXISTS idx_wiki_issue_fingerprint;
DROP INDEX IF EXISTS idx_wiki_page_issues_source_status;
DROP INDEX IF EXISTS idx_wiki_page_issues_page_id;

ALTER TABLE wiki_page_issues ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS resolved_page_version;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS resolution_summary;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS resolution_action;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS resolved_at;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS claimed_at;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS active_attempt_id;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS occurrence_count;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS last_seen_at;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS last_seen_run_id;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS detected_page_version;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS repair_mode;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS evidence;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS fingerprint;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS source;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS severity;
ALTER TABLE wiki_page_issues DROP COLUMN IF EXISTS page_id;

CREATE TABLE IF NOT EXISTS meeting_orchestration_jobs (
  id VARCHAR(36) PRIMARY KEY,
  owner_scope_id VARCHAR(128) NOT NULL,
  status VARCHAR(24) NOT NULL,
  stage VARCHAR(32),
  progress INTEGER NOT NULL DEFAULT 0,
  source_fingerprint VARCHAR(80),
  result_wiki_page_id VARCHAR(64),
  error_code VARCHAR(64),
  error_message TEXT,
  prompt_version VARCHAR(64),
  started_at TIMESTAMP,
  finished_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meeting_jobs_owner_created ON meeting_orchestration_jobs(owner_scope_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_meeting_jobs_status ON meeting_orchestration_jobs(status);
CREATE TABLE IF NOT EXISTS meeting_orchestration_currents (
  owner_scope_id VARCHAR(128) PRIMARY KEY,
  job_id VARCHAR(36) NOT NULL,
  result_wiki_page_id VARCHAR(64) NOT NULL,
  source_fingerprint VARCHAR(80) NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meeting_current_job ON meeting_orchestration_currents(job_id);
CREATE INDEX IF NOT EXISTS idx_meeting_current_wiki ON meeting_orchestration_currents(result_wiki_page_id);

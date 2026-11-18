ALTER TABLE messages DROP COLUMN IF EXISTS run_state;

DROP INDEX IF EXISTS idx_agent_run_suspensions_session;
DROP TABLE IF EXISTS agent_run_suspensions;

DROP INDEX IF EXISTS idx_agent_prompts_message;
DROP INDEX IF EXISTS idx_agent_prompts_status_expires;
DROP INDEX IF EXISTS idx_agent_prompts_status_lease;
DROP INDEX IF EXISTS idx_agent_prompts_session_status;
DROP TABLE IF EXISTS agent_prompts;

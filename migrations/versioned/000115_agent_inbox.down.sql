-- Migration 000115 down: drop the agent inbox.
DROP INDEX IF EXISTS idx_agent_inbox_retention;
DROP INDEX IF EXISTS idx_agent_inbox_owner;
DROP INDEX IF EXISTS idx_agent_inbox_scope;
DROP TABLE IF EXISTS agent_inbox;

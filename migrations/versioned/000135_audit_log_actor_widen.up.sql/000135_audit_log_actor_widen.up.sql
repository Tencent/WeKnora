-- Migration: 000135_audit_log_actor_widen
-- Widen audit_logs.actor_user_id and audit_logs.target_user_id from
-- VARCHAR(36) to VARCHAR(64). Synthetic actor IDs built by channel auth
-- middleware exceed 36 chars: embed channels use "embed-" + channel UUID
-- (42 chars) and MCP endpoints use "mcp-" + endpoint UUID (40 chars), so
-- audit inserts for those identities failed with SQLSTATE 22001
-- (value too long) and the audit rows were silently dropped.
ALTER TABLE audit_logs ALTER COLUMN actor_user_id TYPE VARCHAR(64);
ALTER TABLE audit_logs ALTER COLUMN target_user_id TYPE VARCHAR(64);

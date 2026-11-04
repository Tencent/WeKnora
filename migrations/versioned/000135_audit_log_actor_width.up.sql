-- Migration: 000135_audit_log_actor_width
-- Widens audit_logs.actor_user_id so synthetic principals fit.
--
-- actor_user_id was sized for a bare user UUID (36 chars), but two
-- authentication paths have no users row and mint a prefixed id instead:
--   - embed channels:  "embed-<channel uuid>"  = 42 chars
--     (internal/middleware/embed_auth.go)
--   - MCP endpoints:   "mcp-<endpoint uuid>"   = 40 chars
--     (internal/middleware/mcp_endpoint_auth.go)
-- Both overflow VARCHAR(36), so every audited action performed through them
-- failed with SQLSTATE 22001 (value too long) and was silently dropped by
-- auditLogService.Log, which logs but never propagates insert errors
-- (issue #4829).
--
-- 64 keeps the existing prefixes and leaves headroom for further synthetic
-- principal kinds without another migration.
DO $$ BEGIN RAISE NOTICE '[Migration 000135] Widening audit_logs.actor_user_id to VARCHAR(64)'; END $$;

ALTER TABLE audit_logs ALTER COLUMN actor_user_id TYPE VARCHAR(64);

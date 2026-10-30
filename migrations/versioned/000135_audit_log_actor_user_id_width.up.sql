DO $$ BEGIN RAISE NOTICE '[Migration 000135] Widening audit_logs.actor_user_id...'; END $$;

-- Synthetic principals (embed channel `embed-<uuid>`, MCP endpoint
-- `mcp-<uuid>`) exceed the 36-char width reserved for plain user UUIDs,
-- so their audit rows were rejected with SQLSTATE 22001 and silently lost.
-- Widen the column to fit those prefixes.
ALTER TABLE audit_logs ALTER COLUMN actor_user_id TYPE VARCHAR(64);

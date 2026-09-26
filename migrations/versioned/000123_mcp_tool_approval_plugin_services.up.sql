-- Migration 000123: MCP services plugins provide are not rows of mcp_services,
-- so their tool policies could not be stored. mcp_services is soft-deleted,
-- so the cascade never ran; the tool policy table no longer references it.
DO $$ BEGIN RAISE NOTICE '[Migration 000123] Dropping the mcp_tool_approvals service foreign key'; END $$;

DO $$
DECLARE c RECORD;
BEGIN
    FOR c IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'mcp_tool_approvals'::regclass AND contype = 'f'
          AND confrelid = 'mcp_services'::regclass
    LOOP
        EXECUTE format('ALTER TABLE mcp_tool_approvals DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

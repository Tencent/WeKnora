-- Lite host-sandbox binding: the user-selected project directory this session
-- operates in. NULL/empty means the session gets an auto-allocated workspace.
-- Written once at creation and never updated. Standard edition never writes it.
DO $$ BEGIN RAISE NOTICE '[Migration 000109] Adding sessions.host_workspace_dir'; END $$;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS host_workspace_dir VARCHAR(1024) DEFAULT NULL;
COMMENT ON COLUMN sessions.host_workspace_dir IS 'Lite host sandbox project directory; NULL/empty = auto session workspace; immutable after create';

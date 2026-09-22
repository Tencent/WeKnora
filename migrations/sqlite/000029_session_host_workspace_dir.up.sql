-- Mirrors versioned migration 000109_session_host_workspace_dir:
-- Lite host-sandbox binding: the user-selected project directory this session
-- operates in. NULL/empty means the session gets an auto-allocated workspace.
-- Written once at creation and never updated.

ALTER TABLE sessions ADD COLUMN host_workspace_dir TEXT;

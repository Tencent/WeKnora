-- Workspaces an installed plugin is limited to; NULL for all (versioned 000121).
ALTER TABLE plugins ADD COLUMN audience TEXT;

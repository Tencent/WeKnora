-- Migration 000079: knowledge-base document progress markers.
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS progress_marked BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE knowledges
SET progress_marked = TRUE
WHERE parse_status IN ('pending', 'processing', 'finalizing');

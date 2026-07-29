-- Roll back migration 000079.
ALTER TABLE knowledges DROP COLUMN IF EXISTS progress_marked;

-- Roll back the SQLite knowledge folder migration.
--
-- DROP COLUMN needs SQLite 3.35+ (2021); older builds have to rebuild the
-- table, which is out of scope for a down migration that only discards an
-- organisational layer.
DROP INDEX IF EXISTS idx_knowledges_kb_folder;
ALTER TABLE knowledges DROP COLUMN folder_id;
DROP TABLE IF EXISTS knowledge_folders;

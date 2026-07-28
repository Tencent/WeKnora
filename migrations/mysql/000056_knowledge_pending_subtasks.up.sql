-- MySQL 8 translation of 000056_knowledge_pending_subtasks.up.sql.
-- MySQL has no anonymous DO block; apply its schema change directly.
ALTER TABLE knowledges
    ADD COLUMN pending_subtasks_count INT NOT NULL DEFAULT 0;

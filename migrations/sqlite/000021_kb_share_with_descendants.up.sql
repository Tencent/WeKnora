-- SQLite: mirror 000077_kb_share_with_descendants

ALTER TABLE knowledge_bases
    ADD COLUMN share_with_descendants INTEGER NOT NULL DEFAULT 0;

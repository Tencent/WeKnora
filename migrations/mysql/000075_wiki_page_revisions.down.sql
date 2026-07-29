DROP TABLE IF EXISTS wiki_page_revisions;

ALTER TABLE wiki_pages
    DROP COLUMN last_editor_id,
    DROP COLUMN last_edit_source;

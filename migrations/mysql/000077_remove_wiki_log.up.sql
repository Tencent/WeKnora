-- Knowledge-base activity is now the authoritative mutation history.
DROP TABLE IF EXISTS wiki_log_entries;

DELETE FROM wiki_pages WHERE page_type = 'log';

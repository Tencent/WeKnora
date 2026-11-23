CREATE INDEX IF NOT EXISTS idx_wiki_pages_fulltext
    ON wiki_pages USING GIN (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')));

CREATE INDEX IF NOT EXISTS idx_wiki_pages_source_refs
    ON wiki_pages USING GIN (source_refs jsonb_path_ops);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_source_refs_text
    ON wiki_pages USING GIN (to_tsvector('simple', source_refs::text));

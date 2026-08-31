-- Restore the previous schema default. Existing rows intentionally remain
-- normalized because the data migration is not safely reversible.

ALTER TABLE knowledge_bases
    ALTER COLUMN chunking_config SET DEFAULT '{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}'::jsonb;

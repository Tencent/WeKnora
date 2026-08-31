-- Migration 000091: align persisted chunking configuration with the runtime defaults.

DO $$ BEGIN RAISE NOTICE '[Migration 000091] Normalizing knowledge base chunking configuration'; END $$;

UPDATE knowledge_bases
SET chunking_config =
    (chunking_config - 'split_markers' - 'keep_separator')
    || CASE
        WHEN chunking_config ? 'split_markers'
             AND NOT (chunking_config ? 'separators')
        THEN jsonb_build_object('separators', chunking_config -> 'split_markers')
        ELSE '{}'::jsonb
    END
WHERE chunking_config ? 'split_markers'
   OR chunking_config ? 'keep_separator';

ALTER TABLE knowledge_bases
    ALTER COLUMN chunking_config SET DEFAULT '{"chunk_size": 512, "chunk_overlap": 80, "separators": ["\n\n", "\n", "。"]}'::jsonb;

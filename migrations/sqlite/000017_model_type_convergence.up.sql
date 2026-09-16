-- Model type convergence (SQLite / lite mode).
--
-- Mirrors migrations/versioned/000096_model_type_convergence.up.sql.
-- The VLLM (vision) type folds into KnowledgeQA (ADR 0004): vision was never
-- a separate wire operation, and the VLLM type itself WAS the vision
-- declaration, so every migrated row unconditionally gains text+image input
-- modalities while preserving any others it declared (audio).
--
-- Irreversible by design (no query can reconstruct which KnowledgeQA rows
-- with image input used to be VLLM); down is a no-op. The converged data is
-- readable by pre-000017 application code.
UPDATE models
SET type = 'KnowledgeQA',
    parameters = json_set(
        COALESCE(parameters, '{}'),
        '$.chat.input_modalities',
        (
            SELECT json_group_array(m)
            FROM (
                SELECT 'text' AS m
                UNION ALL
                SELECT 'image' AS m
                UNION ALL
                SELECT je.value
                FROM json_each(
                    COALESCE(json_extract(models.parameters, '$.chat.input_modalities'), json_array())
                ) je
                WHERE je.value NOT IN ('text', 'image')
            )
        )
    )
WHERE type = 'VLLM';

-- Migration 000096: converge model types — the VLLM (vision) type folds into
-- KnowledgeQA (ADR 0004).
--
-- Vision was never a separate wire operation: a vision model is a chat model
-- whose input envelope includes images. The invoke layer already served both
-- through one Chat facet; this migration makes the stored rows agree. The
-- VLLM type itself WAS the vision declaration, so every migrated row
-- unconditionally gains the image input modality — overriding an explicit
-- text-only Chat shard is intended (the type declaration outranks a later
-- uncheck). text is an axiom of the chat facet and is stamped as well; any
-- other modalities the row already declared (audio) are preserved.
--
-- Irreversible by design: after the merge, KnowledgeQA rows with image input
-- include native vision-capable chat models that must not be reverted to a
-- type that no longer exists. Down is a no-op; pre-000096 code reads
-- KnowledgeQA rows with a Chat shard correctly, so rolling the application
-- back leaves the converged data in place.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.tables WHERE table_name = 'models'
    ) THEN
        RAISE NOTICE '[Migration 000096] models absent · skipping';
        RETURN;
    END IF;

    UPDATE models
    SET type = 'KnowledgeQA',
        parameters = jsonb_set(
            COALESCE(parameters::jsonb, '{}'::jsonb),
            '{chat,input_modalities}',
            '["text","image"]'::jsonb || COALESCE(
                (SELECT jsonb_agg(v)
                 FROM jsonb_array_elements_text(
                     jsonb_extract_path(COALESCE(parameters::jsonb, '{}'::jsonb), 'chat', 'input_modalities')
                 ) AS v
                 WHERE v NOT IN ('text', 'image')),
                '[]'::jsonb)
        )
    WHERE type = 'VLLM';

    RAISE NOTICE '[Migration 000096] VLLM rows converged into KnowledgeQA';
END $$;

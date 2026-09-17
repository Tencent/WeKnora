-- Reverse of 000096 is intentionally a no-op (see the up file): the
-- VLLM → KnowledgeQA rewrite is irreversible (ADR 0004). After the merge,
-- KnowledgeQA rows with image input include native vision-capable chat
-- models, so no query can reconstruct which rows used to be VLLM. The
-- converged data is readable by pre-000096 application code.
SELECT 1;

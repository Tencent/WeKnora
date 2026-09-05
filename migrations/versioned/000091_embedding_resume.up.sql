-- Migration 000091: embedding resume support.
--
-- knowledge_embed_progress records which chunks of a knowledge have already
-- been written to the retrieval stores. Document processing commits vectors
-- batch-by-batch; when a task fails partway (e.g. embedding API rate limits)
-- the retry skips chunks listed here instead of restarting from zero.
--
-- knowledges.chunk_fingerprint is a SHA-256 hex hash of the ordered parsed
-- chunk contents, including both child chunks and (when parent-child chunking
-- is enabled) their parent chunks. computeChunkFingerprint in
-- knowledge_process.go feeds each child as "C|<seq>|<len>|<content>" and
-- each parent as "P|<seq>|<len>|<content>". A matching fingerprint on retry
-- means the persisted chunks are still valid and can be resumed; a mismatch
-- triggers a full rebuild (the pre-000080 behavior).

ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS chunk_fingerprint VARCHAR(64) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS knowledge_embed_progress (
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id     VARCHAR(36) NOT NULL,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (knowledge_id, chunk_id)
);

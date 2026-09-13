-- Reverses 000096 by restoring the shape the old memory model had when it was
-- replaced: the item store as 000084 created it and 000094 (replaces_id) and
-- 000095 (the pgvector column and its search index) amended it, plus the topic
-- tracker and the document affinity counters as 000084 left them.
--
-- Shape only, in every case. The statements, the topic counts and the citation
-- counts were accumulated one conversation at a time and nothing retains them,
-- so a rollback lands on an empty store that refills as conversations are
-- distilled again — the state a new install had.

CREATE TABLE IF NOT EXISTS memory_items (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    content TEXT NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    normalized_key VARCHAR(255) NOT NULL DEFAULT '',
    importance SMALLINT NOT NULL DEFAULT 3,
    origin VARCHAR(16) NOT NULL DEFAULT 'extracted',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    source_session_id VARCHAR(36),
    source_message_id VARCHAR(36),
    valid_from TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    invalid_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE,
    superseded_by VARCHAR(36),
    last_used_at TIMESTAMP WITH TIME ZONE,
    use_count INTEGER NOT NULL DEFAULT 0,
    replaces_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_items_scope
    ON memory_items (tenant_id, subject_id, status);
CREATE INDEX IF NOT EXISTS idx_memory_items_key
    ON memory_items (tenant_id, subject_id, normalized_key);
CREATE INDEX IF NOT EXISTS idx_memory_replaces
    ON memory_items (tenant_id, subject_id, replaces_id, status);

CREATE TABLE IF NOT EXISTS memory_tombstones (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    fingerprint VARCHAR(64) NOT NULL,
    source_message_id VARCHAR(36),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_tombstones_scope
    ON memory_tombstones (tenant_id, subject_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_tomb_fp
    ON memory_tombstones (tenant_id, subject_id, fingerprint);

CREATE TABLE IF NOT EXISTS memory_item_embeddings (
    item_id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    dims INTEGER NOT NULL DEFAULT 0,
    vector BYTEA,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_mem_emb_scope
    ON memory_item_embeddings (tenant_id, subject_id);

-- Mirrors 000095: the halfvec column only exists where pgvector does, and the
-- repository scores in process when it is missing.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        RAISE NOTICE '[Migration 000096 down] vector extension absent · memory keeps scoring in process';
    ELSE
        ALTER TABLE memory_item_embeddings ADD COLUMN IF NOT EXISTS embedding halfvec;
    END IF;

    CREATE INDEX IF NOT EXISTS idx_mem_emb_search
        ON memory_item_embeddings (tenant_id, subject_id, model_id, dims);
END $$;

ALTER TABLE memory_subjects ADD COLUMN IF NOT EXISTS block_text TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_subjects ADD COLUMN IF NOT EXISTS block_updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE memory_subjects ADD COLUMN IF NOT EXISTS item_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS memory_topic_stats (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    normalized_key VARCHAR(255) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    aliases JSONB NOT NULL DEFAULT '[]',
    hits INTEGER NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMP WITH TIME ZONE,
    promoted_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_topic_scope
    ON memory_topic_stats (tenant_id, subject_id, normalized_key);

CREATE TABLE IF NOT EXISTS memory_doc_affinity (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    title VARCHAR(512) NOT NULL DEFAULT '',
    hits INTEGER NOT NULL DEFAULT 0,
    last_used_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_affinity_scope
    ON memory_doc_affinity (tenant_id, subject_id, knowledge_id);

DROP TABLE IF EXISTS memory_notes;
DROP TABLE IF EXISTS memory_digests;
DROP TABLE IF EXISTS memory_episode_embeddings;
DROP TABLE IF EXISTS memory_episodes;

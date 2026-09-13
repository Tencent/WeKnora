-- Mirror of versioned 000096: memory becomes documents.
--
-- The item store kept one sanitized sentence per memory and resolved
-- contradictions by superseding rows, which stores a conclusion and drops the
-- conditions it held for. What replaces it keeps whole conversations:
-- memory_episodes holds one account per conversation, memory_digests holds the
-- single consolidated profile every turn injects, and memory_notes holds what
-- the person asked to be remembered word for word.
--
-- Nothing is migrated across. An account is written from a transcript and
-- there is no transcript behind an item, so the store rebuilds itself from
-- conversations as they happen.

-- One conversation's worth of memory, as a document.
CREATE TABLE IF NOT EXISTS memory_episodes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    slug VARCHAR(120) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    outcome VARCHAR(16) NOT NULL DEFAULT 'uncertain',
    summary TEXT NOT NULL,
    keywords TEXT,
    from_at DATETIME,
    to_at DATETIME,
    use_count INTEGER NOT NULL DEFAULT 0,
    last_used_at DATETIME,
    digest_revision INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_episode_slug
    ON memory_episodes (tenant_id, subject_id, slug);
CREATE INDEX IF NOT EXISTS idx_mem_episode_selection
    ON memory_episodes (tenant_id, subject_id, use_count DESC, last_used_at DESC, created_at DESC);
-- Partial, so accounts written without a conversation do not all collide on
-- the empty string.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_episode_session
    ON memory_episodes (tenant_id, subject_id, session_id)
    WHERE session_id <> '';
CREATE INDEX IF NOT EXISTS idx_mem_episode_unconsolidated
    ON memory_episodes (tenant_id, subject_id, digest_revision);

-- Episode vectors, separate so that listing episodes does not drag kilobytes
-- of float along.
CREATE TABLE IF NOT EXISTS memory_episode_embeddings (
    episode_id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    dims INTEGER NOT NULL DEFAULT 0,
    vector BLOB,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mem_ep_emb_search
    ON memory_episode_embeddings (tenant_id, subject_id, model_id, dims);

-- The consolidated profile: one live row per person.
CREATE TABLE IF NOT EXISTS memory_digests (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    previous_body TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 0,
    episode_count INTEGER NOT NULL DEFAULT 0,
    user_edited_at DATETIME,
    generated_at DATETIME,
    lease_id VARCHAR(36) NOT NULL DEFAULT '',
    leased_until DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, subject_id)
);

-- What the user asked to remember, in their words.
CREATE TABLE IF NOT EXISTS memory_notes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    content TEXT NOT NULL,
    source_session_id VARCHAR(36) NOT NULL DEFAULT '',
    source_message_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mem_note_scope
    ON memory_notes (tenant_id, subject_id, created_at DESC);

-- The topic tracker's recurrence signal now comes from counting how many
-- accounts share a keyword, so these counters have no reader.
DROP TABLE IF EXISTS memory_topic_stats;

-- The per-person rerank boost these counters fed measured which documents the
-- retriever kept picking rather than which ones the person found useful, so it
-- fed its own past choices back into itself. Nothing replaces it.
DROP TABLE IF EXISTS memory_doc_affinity;

DROP TABLE IF EXISTS memory_item_embeddings;
DROP TABLE IF EXISTS memory_items;
DROP TABLE IF EXISTS memory_tombstones;

-- The resident block was the rendered item list cached on the subject.
-- memory_digests is that cache now, with its own revision and lease.
ALTER TABLE memory_subjects DROP COLUMN block_text;
ALTER TABLE memory_subjects DROP COLUMN block_updated_at;
ALTER TABLE memory_subjects DROP COLUMN item_count;

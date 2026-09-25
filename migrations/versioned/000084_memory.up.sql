-- Migration 000084: cross-session long-term memory.
--
-- Two tables only. memory_subjects is one row per (workspace, principal) and
-- caches the resident block that every turn injects; memory_items holds the
-- individual remembered statements.
--
-- Contradictions are resolved by bi-temporal supersede rather than deletion:
-- the outdated row keeps its content and gets invalid_at + superseded_by, so
-- "what did it believe last month" stays answerable and the user can see why a
-- memory changed. Rows are only physically removed when the user forgets them.

CREATE TABLE IF NOT EXISTS memory_subjects (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    -- Principal.StorageID(), e.g. "web_user:<uuid>" or "im_user:wecom:<ch>:<u>".
    subject_id VARCHAR(512) NOT NULL,
    -- Per-user opt out. The workspace-level switch lives on tenants.memory_config.
    enabled BOOLEAN NOT NULL DEFAULT true,
    -- Rendered profile/preference block, recomputed on write and read as-is on
    -- every turn so the read path stays a single primary-key lookup.
    block_text TEXT NOT NULL DEFAULT '',
    block_updated_at TIMESTAMP WITH TIME ZONE,
    item_count INTEGER NOT NULL DEFAULT 0,
    last_extracted_at TIMESTAMP WITH TIME ZONE,
    -- Watermark: everything this subject said up to here has been considered
    -- for distillation. Runs walk forward from it, so a message cannot be
    -- skipped by a timer or by a burst of turns.
    extract_cursor TIMESTAMP WITH TIME ZONE,
    -- Sessions with turns past the cursor, recorded when a turn arrives while
    -- a run is already in flight.
    pending_sessions JSONB,
    -- Set while a distillation task is queued or running, so concurrent turns
    -- enqueue one task rather than one per turn.
    extract_scheduled_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_subjects_scope
    ON memory_subjects (tenant_id, subject_id);

CREATE TABLE IF NOT EXISTS memory_items (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    -- profile | preference | fact | task
    kind VARCHAR(32) NOT NULL,
    content TEXT NOT NULL,
    -- Readable subject the statement is about, kept verbatim because a question
    -- often names the topic while the statement carries only the value.
    topic VARCHAR(255) NOT NULL DEFAULT '',
    -- Normalized topic key used to detect that a new statement contradicts an
    -- existing one. Two items sharing a key are the same fact at different times.
    normalized_key VARCHAR(255) NOT NULL DEFAULT '',
    importance SMALLINT NOT NULL DEFAULT 3,
    -- explicit (user asked) | extracted (background) | manual (memory editor)
    origin VARCHAR(16) NOT NULL DEFAULT 'extracted',
    -- active | superseded | archived
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    source_session_id VARCHAR(36),
    source_message_id VARCHAR(36),
    valid_from TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    invalid_at TIMESTAMP WITH TIME ZONE,
    -- When the statement stops being worth recalling. Without it an in-flight
    -- task stays in context long after the user finished it.
    expires_at TIMESTAMP WITH TIME ZONE,
    superseded_by VARCHAR(36),
    last_used_at TIMESTAMP WITH TIME ZONE,
    use_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_items_scope
    ON memory_items (tenant_id, subject_id, status);
CREATE INDEX IF NOT EXISTS idx_memory_items_key
    ON memory_items (tenant_id, subject_id, normalized_key);

-- A statement the user deliberately forgot. Only the topic and a fingerprint of
-- the statement are kept, never the statement itself: this table exists to stop
-- distillation from re-adding what was dropped, not to retain it.
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

-- How often this person has asked about a topic. A single question is noise;
-- the same subject across several conversations is a signal, so topics are
-- counted here and only promoted into memory_items once they recur.
CREATE TABLE IF NOT EXISTS memory_topic_stats (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    normalized_key VARCHAR(255) NOT NULL,
    topic VARCHAR(255) NOT NULL DEFAULT '',
    hits INTEGER NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMP WITH TIME ZONE,
    promoted_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_topic_scope
    ON memory_topic_stats (tenant_id, subject_id, normalized_key);

-- How often this person's answers drew on a document. Read by the reranker to
-- prefer material they keep coming back to.
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

-- Workspace-level switch, write mode, extraction model and capacity.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS memory_config JSONB;

-- Which memories were injected into this answer. Persisted rather than only
-- streamed so reopening a conversation still shows what the answer saw.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS used_memories JSONB;

ALTER TABLE memory_subjects ADD COLUMN IF NOT EXISTS consolidated_at TIMESTAMP WITH TIME ZONE;

-- A review someone asked for is rate limited separately from the daily pass:
-- one clock for both would let the daily pass refuse the button.
ALTER TABLE memory_subjects ADD COLUMN IF NOT EXISTS forced_consolidated_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE memory_topic_stats ADD COLUMN IF NOT EXISTS aliases JSONB NOT NULL DEFAULT '[]';

-- Vectors live apart from the items so that listing, capacity enforcement and
-- the resident block never pay to load them.
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

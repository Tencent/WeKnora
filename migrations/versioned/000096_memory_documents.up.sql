-- Migration 000096: memory becomes documents.
--
-- The store this replaces kept one sanitized sentence per memory, keyed by a
-- normalized topic, and resolved contradictions by superseding rows. That model
-- has a ceiling it cannot be tuned past: a conclusion with its conditions
-- stripped off ("要求先给方案") cannot be applied safely later, because nothing
-- records what it held for, and there is no second layer to go look. A store of
-- three hundred such phrases reads like a tag cloud.
--
-- What replaces it is the arrangement Codex's memory pipeline converged on:
--
--   memory_episodes  one faithful account per conversation, written as a
--                    document — tasks in the order they happened, what the
--                    user asked and corrected in their own words, what was
--                    concluded, what is still open. This is the detail layer,
--                    reached on demand.
--
--   memory_digests   one consolidated profile per person, rewritten from the
--                    episodes rather than accumulated: who they are, how they
--                    work, and an index into the episodes worth opening. This
--                    is the layer injected into a turn, and it is small on
--                    purpose because it is mostly pointers.
--
--   memory_notes     what the user explicitly asked to remember, verbatim.
--                    Never rewritten by a model: a person who typed
--                    "记住：我只用中文" is owed those words back, not a
--                    paraphrase of them.
--
-- Retention is driven by use rather than by importance. An episode that keeps
-- being read stays selected for consolidation; one nothing has needed in weeks
-- falls out. Importance was always a guess about relevance made before the
-- question was known.
--
-- The three tables the old model needed go with it, at the bottom of this file:
-- the items themselves, the topic tracker that indexed them, and the document
-- affinity counters that conditioned retrieval on them.

-- One conversation's worth of memory, as a document.
CREATE TABLE IF NOT EXISTS memory_episodes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    -- The conversation this account is of. One episode per conversation: as
    -- the conversation continues, the account is rewritten rather than
    -- appended to, because two accounts of one conversation is how the digest
    -- ends up citing the same work twice in different words.
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    -- Slug is the stable handle the digest's index points at. Assigned once
    -- and kept across rewrites, so a pointer stays valid while the account
    -- behind it improves. Unique per person: a pointer that can resolve to two
    -- documents is not a pointer.
    slug VARCHAR(120) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    -- success | partial | fail | uncertain. What the conversation actually
    -- achieved, judged from the transcript. A later reader needs it to know
    -- whether an approach recorded here is one to repeat.
    outcome VARCHAR(16) NOT NULL DEFAULT 'uncertain',
    summary TEXT NOT NULL,
    -- Retrieval handles: tool names, error strings, document titles, concepts.
    keywords JSONB,
    -- The stretch of conversation this account covers.
    from_at TIMESTAMP WITH TIME ZONE,
    to_at TIMESTAMP WITH TIME ZONE,
    -- Use, not importance, decides what survives.
    use_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TIMESTAMP WITH TIME ZONE,
    -- The digest revision that last consolidated this episode. It is how the
    -- next consolidation tells what is new without diffing prose.
    digest_revision BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_episode_slug
    ON memory_episodes (tenant_id, subject_id, slug);
-- Consolidation's selection order: most used first, then most recently useful.
CREATE INDEX IF NOT EXISTS idx_mem_episode_selection
    ON memory_episodes (tenant_id, subject_id, use_count DESC, last_used_at DESC, created_at DESC);
-- One account per conversation, enforced rather than assumed. Partial so that
-- accounts written without a session — a manual note, an import — do not all
-- collide on the empty string.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_episode_session
    ON memory_episodes (tenant_id, subject_id, session_id)
    WHERE session_id <> '';
-- Episodes not yet folded into the digest: the dirty check that decides
-- whether a consolidation call has anything to do.
CREATE INDEX IF NOT EXISTS idx_mem_episode_unconsolidated
    ON memory_episodes (tenant_id, subject_id, digest_revision);

-- Episode vectors, in their own table for the same reason item vectors were:
-- the manager, the digest and retention all list episodes constantly and none
-- of them want to drag kilobytes of float along.
CREATE TABLE IF NOT EXISTS memory_episode_embeddings (
    episode_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    dims INTEGER NOT NULL DEFAULT 0,
    vector BYTEA,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mem_ep_emb_search
    ON memory_episode_embeddings (tenant_id, subject_id, model_id, dims);

-- The consolidated profile: one live row per person.
CREATE TABLE IF NOT EXISTS memory_digests (
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    -- The document injected into a turn.
    body TEXT NOT NULL DEFAULT '',
    -- The previous body, kept so consolidation can be told what it changed
    -- last time and so a bad rewrite is recoverable. This is the cheap version
    -- of the git baseline Codex keeps under its memories root.
    previous_body TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 0,
    episode_count INTEGER NOT NULL DEFAULT 0,
    -- When the person edited the digest themselves. A rewrite must respect an
    -- edit rather than restore what they removed, and it cannot do that
    -- without knowing one happened.
    user_edited_at TIMESTAMP WITH TIME ZONE,
    generated_at TIMESTAMP WITH TIME ZONE,
    -- Serializes consolidation the way the extraction lease serializes
    -- distillation: one rewrite of a shared document at a time.
    lease_id VARCHAR(36) NOT NULL DEFAULT '',
    leased_until TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (tenant_id, subject_id)
);

-- What the user asked to remember, in their words.
CREATE TABLE IF NOT EXISTS memory_notes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    content TEXT NOT NULL,
    source_session_id VARCHAR(36) NOT NULL DEFAULT '',
    source_message_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mem_note_scope
    ON memory_notes (tenant_id, subject_id, created_at DESC);

-- pgvector, if this deployment has it. Same arrangement as 000095: the BYTEA
-- blob stays the source of truth so a deployment without the extension keeps
-- working, scoring in process instead.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        RAISE NOTICE '[Migration 000096] vector extension absent · episodes score in process';
    ELSE
        ALTER TABLE memory_episode_embeddings ADD COLUMN IF NOT EXISTS embedding halfvec;
        RAISE NOTICE '[Migration 000096] memory_episode_embeddings.embedding ready';
    END IF;
END $$;

-- Retire the topic tracker.
--
-- It answered one question: which subjects does this person keep coming back
-- to. It answered it by asking a model to name each conversation's subject,
-- reducing that name to an identity key by deleting characters guessed to be
-- uninformative, matching the key against existing rows by character-bigram
-- overlap, and spending a second model call to adjudicate the near-misses.
-- Every layer was a chance to decide two unrelated subjects were one, and a
-- wrong merge surfaced only as a climbing counter on a subject nobody named.
--
-- The accounts above already carry keywords, lifted verbatim from the
-- conversation rather than invented for it. Counting how many accounts share a
-- keyword is the same signal without the guessing, so the recurrence question
-- is now a GROUP BY and this table has no reader.
DROP TABLE IF EXISTS memory_topic_stats;

-- Retire document affinity.
--
-- It counted how often a person's answers cited each document and turned that
-- into a small multiplier in the reranker. The signal it actually measured is
-- not the one it claimed: a document appearing in past answers means the
-- retriever kept picking it, not that the person found it useful. Boosting on
-- that is a feedback loop — the retriever's own past choices become a reason to
-- choose the same documents again, and the documents it never surfaced never
-- get the chance to earn a count. The logarithmic curve slowed the loop down
-- without breaking it.
--
-- Nothing replaces it. Which passages answer a question is a question the
-- reranker already answers from the question, and who is asking is applied
-- where it belongs: the consolidated profile conditions query rewriting, and
-- the matched accounts are injected into the turn. The Wiki graph's "familiar
-- document" highlight read the same counters and goes with them; it had no
-- other source for that overlay.
DROP TABLE IF EXISTS memory_doc_affinity;

-- Retire the atomic item store.
--
-- Memory used to be a few hundred one-line statements per person, ranked
-- against the question and injected a handful at a time. What it remembered was
-- true and useless: "用户用 PostgreSQL" survives the conversation that produced
-- it, but not the reason, the alternative that was rejected, or the constraint
-- that made it necessary, so an answer built on it could restate a preference
-- without being able to act on it.
--
-- Nothing is migrated across. An account is a narrative written from a
-- transcript, and there is no transcript behind an item: the rows recorded a
-- conclusion and dropped the evidence. Synthesising accounts from them would
-- manufacture history, so the items are dropped and the store rebuilds itself
-- from conversations as they happen.
DROP TABLE IF EXISTS memory_item_embeddings;
DROP TABLE IF EXISTS memory_items;

-- Rejections were remembered so distillation could not re-derive a statement
-- the user had just deleted. An account is not re-derived: it is rewritten in
-- place for its own conversation and deleted outright when the user deletes it,
-- so there is nothing for a fingerprint to guard against.
DROP TABLE IF EXISTS memory_tombstones;

-- The resident block was the rendered item list, cached on the subject so the
-- read path stayed one primary-key lookup. memory_digests is that cache now,
-- with its own revision and lease, and it is the model rather than a renderer
-- that decides what the block says.
ALTER TABLE memory_subjects DROP COLUMN IF EXISTS block_text;
ALTER TABLE memory_subjects DROP COLUMN IF EXISTS block_updated_at;
ALTER TABLE memory_subjects DROP COLUMN IF EXISTS item_count;

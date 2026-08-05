-- Migration: 000079_wiki_provenance
-- Description: Add versioned, block-level provenance for generated Wiki content.
--
-- This migration is intentionally additive. Existing source_refs/chunk_refs stay
-- in place while the ingest and delete paths are moved to the new source ledger.

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating Wiki provenance schema...'; END $$;

-- ---------------------------------------------------------------------------
-- 1) Immutable source-document revisions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS knowledge_revisions (
    id                VARCHAR(64) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    revision_no       INT NOT NULL,
    parse_attempt     INT NOT NULL DEFAULT 0,
    status            VARCHAR(32) NOT NULL DEFAULT 'staged',
    content_hash      VARCHAR(64) NOT NULL DEFAULT '',
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    published_at      TIMESTAMP WITH TIME ZONE,
    superseded_at     TIMESTAMP WITH TIME ZONE,
    deleted_at        TIMESTAMP WITH TIME ZONE,
    CONSTRAINT chk_knowledge_revision_no CHECK (revision_no > 0),
    CONSTRAINT chk_knowledge_revision_attempt CHECK (parse_attempt >= 0),
    CONSTRAINT chk_knowledge_revision_status CHECK (
        status IN ('staged', 'published', 'failed', 'superseded', 'deleted')
    ),
    CONSTRAINT uq_knowledge_revision_number UNIQUE (knowledge_id, revision_no)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_revisions_tenant_kb
    ON knowledge_revisions(tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_revisions_knowledge
    ON knowledge_revisions(knowledge_id, revision_no DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_revisions_published
    ON knowledge_revisions(knowledge_id)
    WHERE status = 'published' AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- 2) Immutable Wiki page revisions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_provenance_page_revisions (
    id                VARCHAR(64) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id           VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    publish_key       VARCHAR(128) NOT NULL DEFAULT '',
    publish_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    revision_no       INT NOT NULL,
    status            VARCHAR(32) NOT NULL DEFAULT 'staged',
    title             VARCHAR(512) NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    rendered_content  TEXT NOT NULL DEFAULT '',
    content_hash      VARCHAR(64) NOT NULL DEFAULT '',
    provenance_status VARCHAR(32) NOT NULL DEFAULT 'partial',
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    published_at      TIMESTAMP WITH TIME ZONE,
    superseded_at     TIMESTAMP WITH TIME ZONE,
    deleted_at        TIMESTAMP WITH TIME ZONE,
    CONSTRAINT chk_wiki_page_revision_no CHECK (revision_no > 0),
    CONSTRAINT chk_wiki_page_revision_status CHECK (
        status IN ('staged', 'published', 'failed', 'superseded', 'deleted')
    ),
    CONSTRAINT chk_wiki_page_revision_provenance CHECK (
        provenance_status IN ('verified', 'partial', 'unsupported', 'legacy_inferred')
    ),
    CONSTRAINT uq_wiki_page_revision_number UNIQUE (page_id, revision_no)
);

CREATE INDEX IF NOT EXISTS idx_wiki_provenance_page_revisions_tenant_kb
    ON wiki_provenance_page_revisions(tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_provenance_page_revisions_page
    ON wiki_provenance_page_revisions(page_id, revision_no DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wiki_provenance_page_revisions_published
    ON wiki_provenance_page_revisions(page_id)
    WHERE status = 'published' AND deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_wiki_provenance_page_revisions_publish_key
    ON wiki_provenance_page_revisions(tenant_id, page_id, publish_key)
    WHERE publish_key <> '' AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- 3) Structured content blocks. Markdown/HTML is a rendered projection.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_page_blocks (
    id                VARCHAR(64) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id           VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    page_revision_id  VARCHAR(64) NOT NULL REFERENCES wiki_provenance_page_revisions(id) ON DELETE CASCADE,
    logical_block_id  VARCHAR(64) NOT NULL,
    parent_block_id   VARCHAR(64) REFERENCES wiki_page_blocks(id) ON DELETE SET NULL,
    block_type        VARCHAR(32) NOT NULL,
    sort_order        INT NOT NULL DEFAULT 0,
    content           TEXT NOT NULL DEFAULT '',
    content_hash      VARCHAR(64) NOT NULL DEFAULT '',
    author_type       VARCHAR(32) NOT NULL DEFAULT 'generated',
    provenance_status VARCHAR(32) NOT NULL DEFAULT 'partial',
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_wiki_block_sort_order CHECK (sort_order >= 0),
    CONSTRAINT chk_wiki_block_type CHECK (
        block_type IN (
            'document', 'title', 'summary', 'heading', 'paragraph', 'fact',
            'list_item', 'table_row', 'quote', 'code', 'other'
        )
    ),
    CONSTRAINT chk_wiki_block_author CHECK (
        author_type IN ('generated', 'manual', 'agent', 'unknown')
    ),
    CONSTRAINT chk_wiki_block_provenance CHECK (
        provenance_status IN ('verified', 'partial', 'unsupported', 'legacy_inferred')
    ),
    CONSTRAINT uq_wiki_page_logical_block UNIQUE (page_revision_id, logical_block_id)
);

CREATE INDEX IF NOT EXISTS idx_wiki_page_blocks_page_revision
    ON wiki_page_blocks(page_revision_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_wiki_page_blocks_page
    ON wiki_page_blocks(page_id, logical_block_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_blocks_parent
    ON wiki_page_blocks(parent_block_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_blocks_provenance
    ON wiki_page_blocks(knowledge_base_id, provenance_status);

-- ---------------------------------------------------------------------------
-- 4) Block-to-source evidence ledger (many-to-many)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_block_sources (
    id                    VARCHAR(64) PRIMARY KEY,
    tenant_id             BIGINT NOT NULL,
    knowledge_base_id     VARCHAR(36) NOT NULL,
    page_id               VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    block_id              VARCHAR(64) NOT NULL REFERENCES wiki_page_blocks(id) ON DELETE CASCADE,
    knowledge_id          VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    knowledge_revision_id VARCHAR(64) NOT NULL REFERENCES knowledge_revisions(id) ON DELETE CASCADE,
    chunk_id              VARCHAR(36) REFERENCES chunks(id) ON DELETE CASCADE,
    source_start          INT NOT NULL DEFAULT -1,
    source_end            INT NOT NULL DEFAULT -1,
    evidence_hash         VARCHAR(64) NOT NULL DEFAULT '',
    source_role           VARCHAR(32) NOT NULL DEFAULT 'supporting',
    confidence            DOUBLE PRECISION NOT NULL DEFAULT 0,
    validation_status     VARCHAR(32) NOT NULL DEFAULT 'pending',
    metadata              JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_wiki_source_offsets CHECK (
        (source_start = -1 AND source_end = -1)
        OR (source_start >= 0 AND source_end >= source_start)
    ),
    CONSTRAINT chk_wiki_source_confidence CHECK (confidence >= 0 AND confidence <= 1),
    CONSTRAINT chk_wiki_source_role CHECK (
        source_role IN ('supporting', 'context', 'contradicting', 'supplementary')
    ),
    CONSTRAINT chk_wiki_source_validation CHECK (
        validation_status IN ('pending', 'verified', 'invalid', 'legacy_inferred')
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wiki_block_sources_evidence
    ON wiki_block_sources(
        block_id,
        knowledge_revision_id,
        COALESCE(chunk_id, ''),
        source_start,
        source_end
    );
CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_block
    ON wiki_block_sources(block_id);
CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_knowledge
    ON wiki_block_sources(knowledge_id, page_id);
CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_revision
    ON wiki_block_sources(knowledge_revision_id);
CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_chunk
    ON wiki_block_sources(chunk_id)
    WHERE chunk_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_tenant_kb
    ON wiki_block_sources(tenant_id, knowledge_base_id);

-- ---------------------------------------------------------------------------
-- 5) Page-level source summary for fast impact analysis. The block ledger is
-- the source of truth; this table is a transactionally maintained projection.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_page_sources (
    tenant_id                  BIGINT NOT NULL,
    knowledge_base_id          VARCHAR(36) NOT NULL,
    page_id                    VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    knowledge_id               VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    supported_block_count      INT NOT NULL DEFAULT 0,
    last_knowledge_revision_id VARCHAR(64) REFERENCES knowledge_revisions(id) ON DELETE SET NULL,
    mapping_granularity        VARCHAR(16) NOT NULL DEFAULT 'page',
    validation_status          VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at                 TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, knowledge_id),
    CONSTRAINT chk_wiki_page_source_block_count CHECK (supported_block_count >= 0),
    CONSTRAINT chk_wiki_page_source_granularity CHECK (
        mapping_granularity IN ('page', 'block', 'mixed')
    ),
    CONSTRAINT chk_wiki_page_source_validation CHECK (
        validation_status IN ('pending', 'verified', 'invalid', 'legacy_inferred')
    )
);

CREATE INDEX IF NOT EXISTS idx_wiki_page_sources_knowledge
    ON wiki_page_sources(knowledge_id, page_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_sources_tenant_kb
    ON wiki_page_sources(tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_sources_revision
    ON wiki_page_sources(last_knowledge_revision_id)
    WHERE last_knowledge_revision_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 6) Honest legacy backfill
-- ---------------------------------------------------------------------------

-- Existing knowledge rows become immutable revision 1 snapshots. Deleted rows
-- are retained as revision metadata but are not marked published.
INSERT INTO knowledge_revisions (
    id, tenant_id, knowledge_base_id, knowledge_id, revision_no,
    parse_attempt, status, content_hash, created_at, published_at, deleted_at
)
SELECT
    'legacy:' || k.id,
    k.tenant_id,
    k.knowledge_base_id,
    k.id,
    1,
    0,
    CASE WHEN k.deleted_at IS NULL THEN 'published' ELSE 'deleted' END,
    COALESCE(k.file_hash, ''),
    k.created_at,
    CASE WHEN k.deleted_at IS NULL THEN COALESCE(k.processed_at, k.updated_at) ELSE NULL END,
    k.deleted_at
FROM knowledges k
ON CONFLICT (knowledge_id, revision_no) DO NOTHING;

-- Existing live pages become one published legacy revision. Their body is a
-- single document block because the old schema cannot prove sentence-level
-- attribution.
INSERT INTO wiki_provenance_page_revisions (
    id, tenant_id, knowledge_base_id, page_id, publish_fingerprint, revision_no, status,
    title, summary, rendered_content, content_hash, provenance_status,
    created_at, published_at
)
SELECT
    'legacy:' || p.id,
    p.tenant_id,
    p.knowledge_base_id,
    p.id,
    md5(COALESCE(p.content, '')),
    GREATEST(COALESCE(p.version, 1), 1),
    'published',
    COALESCE(p.title, ''),
    COALESCE(p.summary, ''),
    COALESCE(p.content, ''),
    md5(COALESCE(p.content, '')),
    'legacy_inferred',
    p.created_at,
    p.updated_at
FROM wiki_pages p
WHERE p.deleted_at IS NULL
ON CONFLICT (page_id, revision_no) DO NOTHING;

INSERT INTO wiki_page_blocks (
    id, tenant_id, knowledge_base_id, page_id, page_revision_id,
    logical_block_id, block_type, sort_order, content, content_hash,
    author_type, provenance_status, created_at, updated_at
)
SELECT
    'legacy:' || p.id,
    p.tenant_id,
    p.knowledge_base_id,
    p.id,
    'legacy:' || p.id,
    'legacy-body',
    'document',
    0,
    COALESCE(p.content, ''),
    md5(COALESCE(p.content, '')),
    'unknown',
    'legacy_inferred',
    p.created_at,
    p.updated_at
FROM wiki_pages p
JOIN wiki_provenance_page_revisions r ON r.id = 'legacy:' || p.id
WHERE p.deleted_at IS NULL
ON CONFLICT (page_revision_id, logical_block_id) DO NOTHING;

-- source_refs provides only page-level attribution. Preserve it as such and
-- mark it legacy_inferred rather than pretending it identifies exact text.
INSERT INTO wiki_page_sources (
    tenant_id, knowledge_base_id, page_id, knowledge_id,
    supported_block_count, last_knowledge_revision_id,
    mapping_granularity, validation_status, created_at, updated_at
)
SELECT DISTINCT
    p.tenant_id,
    p.knowledge_base_id,
    p.id,
    k.id,
    0,
    kr.id,
    'page',
    'legacy_inferred',
    p.created_at,
    p.updated_at
FROM wiki_pages p
CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(p.source_refs, '[]'::jsonb)) AS ref(value)
JOIN knowledges k
    ON k.id = split_part(ref.value, '|', 1)
   AND k.tenant_id = p.tenant_id
   AND k.knowledge_base_id = p.knowledge_base_id
JOIN knowledge_revisions kr
    ON kr.knowledge_id = k.id
   AND kr.revision_no = 1
WHERE p.deleted_at IS NULL
ON CONFLICT (page_id, knowledge_id) DO NOTHING;

-- chunk_refs can support a coarse block-level mapping. The whole legacy body
-- is one block, confidence remains 0, and validation stays legacy_inferred.
INSERT INTO wiki_block_sources (
    id, tenant_id, knowledge_base_id, page_id, block_id,
    knowledge_id, knowledge_revision_id, chunk_id,
    source_start, source_end, evidence_hash, source_role,
    confidence, validation_status, created_at
)
SELECT DISTINCT
    'legacy:' || md5(p.id || ':' || c.id),
    p.tenant_id,
    p.knowledge_base_id,
    p.id,
    'legacy:' || p.id,
    c.knowledge_id,
    kr.id,
    c.id,
    -1,
    -1,
    COALESCE(c.content_hash, ''),
    'supporting',
    0,
    'legacy_inferred',
    p.updated_at
FROM wiki_pages p
CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(p.chunk_refs, '[]'::jsonb)) AS ref(value)
JOIN chunks c
    ON c.id = ref.value
   AND c.tenant_id = p.tenant_id
   AND c.knowledge_base_id = p.knowledge_base_id
   AND c.deleted_at IS NULL
JOIN knowledge_revisions kr
    ON kr.knowledge_id = c.knowledge_id
   AND kr.revision_no = 1
JOIN wiki_page_blocks b
    ON b.id = 'legacy:' || p.id
WHERE p.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- Ensure chunk-derived sources also appear in the page-level impact index.
INSERT INTO wiki_page_sources (
    tenant_id, knowledge_base_id, page_id, knowledge_id,
    supported_block_count, last_knowledge_revision_id,
    mapping_granularity, validation_status, created_at, updated_at
)
SELECT
    s.tenant_id,
    s.knowledge_base_id,
    s.page_id,
    s.knowledge_id,
    COUNT(DISTINCT s.block_id)::INT,
    MAX(s.knowledge_revision_id),
    'block',
    'legacy_inferred',
    MIN(s.created_at),
    MAX(s.created_at)
FROM wiki_block_sources s
GROUP BY s.tenant_id, s.knowledge_base_id, s.page_id, s.knowledge_id
ON CONFLICT (page_id, knowledge_id) DO UPDATE SET
    supported_block_count = EXCLUDED.supported_block_count,
    last_knowledge_revision_id = EXCLUDED.last_knowledge_revision_id,
    mapping_granularity = 'block',
    validation_status = 'legacy_inferred',
    updated_at = EXCLUDED.updated_at;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Wiki provenance schema ready'; END $$;

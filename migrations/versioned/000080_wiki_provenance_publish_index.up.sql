-- Migration: 000080_wiki_provenance_publish_index
-- Description: Speed up immutable source revision reuse during atomic Wiki publication.

CREATE INDEX IF NOT EXISTS idx_knowledge_revisions_content_hash
    ON knowledge_revisions(
        tenant_id,
        knowledge_base_id,
        knowledge_id,
        content_hash,
        parse_attempt,
        revision_no DESC
    );

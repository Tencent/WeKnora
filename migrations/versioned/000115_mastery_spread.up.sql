-- Migration 000115: neighbour-spread credit for the knowledge-guidance overlay.
--
-- Reading one page credits the pages adjacent to it with a fraction of the
-- reader's time, so a node the person has not opened yet still picks up a little
-- context from the pages around it. Only *reading time* spreads: a view that
-- merely registered a count (a few seconds of glancing) turns into a few seconds
-- of credit, i.e. nothing at scoring time.
--
-- One row per (subject, kb, slug, day). spread_seconds stores the source page's
-- reading seconds undiscounted; the discount factor and the cap are scoring
-- constants (mastery.Config), applied when the water level is computed, so they
-- can be retuned without rewriting history.

CREATE TABLE IF NOT EXISTS memory_spread_views (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL,
    event_date        DATE NOT NULL,
    spread_seconds    BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_spread_scope
    ON memory_spread_views (tenant_id, subject_id, knowledge_base_id, slug, event_date);


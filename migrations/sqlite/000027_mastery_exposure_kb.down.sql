-- Down for Lite 000027: drop the knowledge_base_id column.
-- SQLite does not support DROP COLUMN before 3.35; recreate the table without it.

CREATE TABLE memory_guide_exposures_new (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    session_id        VARCHAR(36),
    trigger_slug      VARCHAR(255),
    candidate_slug    VARCHAR(255),
    strategy          VARCHAR(32),
    rank              INTEGER,
    shown_at          DATETIME,
    clicked_at        DATETIME,
    qualified_view_at DATETIME
);

INSERT INTO memory_guide_exposures_new (id, tenant_id, subject_id, session_id, trigger_slug, candidate_slug, strategy, rank, shown_at, clicked_at, qualified_view_at)
    SELECT id, tenant_id, subject_id, session_id, trigger_slug, candidate_slug, strategy, rank, shown_at, clicked_at, qualified_view_at
    FROM memory_guide_exposures;

DROP TABLE memory_guide_exposures;
ALTER TABLE memory_guide_exposures_new RENAME TO memory_guide_exposures;

CREATE INDEX IF NOT EXISTS idx_mastery_exposure_scope
    ON memory_guide_exposures (tenant_id, subject_id, shown_at);

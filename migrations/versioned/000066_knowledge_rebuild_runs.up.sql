-- Durable coordination state for incremental knowledge rebuild attempts.
CREATE TABLE IF NOT EXISTS knowledge_rebuild_runs (
    id                         VARCHAR(36) PRIMARY KEY,
    tenant_id                  BIGINT NOT NULL,
    knowledge_id               VARCHAR(36) NOT NULL,
    attempt                    INTEGER NOT NULL DEFAULT 0,
    status                     VARCHAR(32) NOT NULL,
    old_parse_status           VARCHAR(32) NOT NULL DEFAULT '',
    old_enable_status          VARCHAR(32) NOT NULL DEFAULT '',
    old_embedding_model_id     VARCHAR(64) NOT NULL DEFAULT '',
    old_chunk_count            INTEGER NOT NULL DEFAULT 0,
    old_config_fingerprint     VARCHAR(64) NOT NULL DEFAULT '',
    new_config_fingerprint     VARCHAR(64) NOT NULL DEFAULT '',
    parse_cache_key            VARCHAR(128) NOT NULL DEFAULT '',
    parse_cache_hit            BOOLEAN NOT NULL DEFAULT FALSE,
    candidate_chunks           INTEGER NOT NULL DEFAULT 0,
    unchanged_chunks           INTEGER NOT NULL DEFAULT 0,
    metadata_only_chunks       INTEGER NOT NULL DEFAULT 0,
    changed_new_chunks         INTEGER NOT NULL DEFAULT 0,
    stale_chunks               INTEGER NOT NULL DEFAULT 0,
    chunk_diff_ready_at        TIMESTAMP WITH TIME ZONE,
    images_total               INTEGER NOT NULL DEFAULT 0,
    images_completed           INTEGER NOT NULL DEFAULT 0,
    images_failed              INTEGER NOT NULL DEFAULT 0,
    ocr_cache_hits             INTEGER NOT NULL DEFAULT 0,
    caption_cache_hits         INTEGER NOT NULL DEFAULT 0,
    artifacts_total            INTEGER NOT NULL DEFAULT 0,
    artifacts_completed        INTEGER NOT NULL DEFAULT 0,
    artifacts_failed           INTEGER NOT NULL DEFAULT 0,
    summary_required           BOOLEAN NOT NULL DEFAULT FALSE,
    wiki_reduce_required       BOOLEAN NOT NULL DEFAULT FALSE,
    stale_cleanup_at           TIMESTAMP WITH TIME ZONE,
    wiki_reduce_enqueued_at    TIMESTAMP WITH TIME ZONE,
    commit_completed_at        TIMESTAMP WITH TIME ZONE,
    wiki_completed_at          TIMESTAMP WITH TIME ZONE,
    error_message              TEXT NOT NULL DEFAULT '',
    started_at                 TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    artifacts_ready_at         TIMESTAMP WITH TIME ZONE,
    completed_at               TIMESTAMP WITH TIME ZONE,
    updated_at                 TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rebuild_run_knowledge_status
    ON knowledge_rebuild_runs(tenant_id, knowledge_id, status);
CREATE INDEX IF NOT EXISTS idx_rebuild_run_attempt
    ON knowledge_rebuild_runs(tenant_id, knowledge_id, attempt);

CREATE TABLE IF NOT EXISTS knowledge_rebuild_chunk_results (
    id                   VARCHAR(36) PRIMARY KEY,
    run_id               VARCHAR(36) NOT NULL REFERENCES knowledge_rebuild_runs(id) ON DELETE CASCADE,
    chunk_id             VARCHAR(36) NOT NULL,
    chunk_type           VARCHAR(20) NOT NULL,
    classification       VARCHAR(24) NOT NULL,
    content_fingerprint  VARCHAR(64) NOT NULL DEFAULT '',
    metadata_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    created_at           TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(run_id, chunk_id)
);

CREATE INDEX IF NOT EXISTS idx_rebuild_chunk_results_run_class
    ON knowledge_rebuild_chunk_results(run_id, classification);
CREATE INDEX IF NOT EXISTS idx_rebuild_chunk_results_chunk
    ON knowledge_rebuild_chunk_results(chunk_id);

CREATE TABLE IF NOT EXISTS knowledge_rebuild_artifact_results (
    id            VARCHAR(36) PRIMARY KEY,
    run_id        VARCHAR(36) NOT NULL REFERENCES knowledge_rebuild_runs(id) ON DELETE CASCADE,
    stage         VARCHAR(24) NOT NULL,
    artifact_key  VARCHAR(128) NOT NULL,
    status        VARCHAR(16) NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(run_id, stage, artifact_key)
);

CREATE INDEX IF NOT EXISTS idx_rebuild_artifact_results_run_status
    ON knowledge_rebuild_artifact_results(run_id, status);

CREATE TABLE IF NOT EXISTS knowledge_rebuild_image_results (
    id                  VARCHAR(36) PRIMARY KEY,
    run_id              VARCHAR(36) NOT NULL REFERENCES knowledge_rebuild_runs(id) ON DELETE CASCADE,
    image_index         INTEGER NOT NULL,
    status              VARCHAR(16) NOT NULL,
    ocr_cache_key       VARCHAR(128) NOT NULL DEFAULT '',
    caption_cache_key   VARCHAR(128) NOT NULL DEFAULT '',
    ocr_cache_hit       BOOLEAN NOT NULL DEFAULT FALSE,
    caption_cache_hit   BOOLEAN NOT NULL DEFAULT FALSE,
    error_message       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(run_id, image_index)
);

CREATE INDEX IF NOT EXISTS idx_rebuild_image_results_run
    ON knowledge_rebuild_image_results(run_id);

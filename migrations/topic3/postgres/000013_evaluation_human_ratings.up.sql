CREATE TABLE IF NOT EXISTS evaluation_human_ratings (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_id VARCHAR(128) NOT NULL,
    sample_index INTEGER NOT NULL,
    revision INTEGER NOT NULL,
    rater_id VARCHAR(128) NOT NULL,
    rubric_key VARCHAR(64) NOT NULL,
    rubric_version VARCHAR(32) NOT NULL,
    rubric_snapshot JSONB NOT NULL,
    score INTEGER NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    supersedes_id VARCHAR(36),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT evaluation_human_ratings_question_fk
        FOREIGN KEY (tenant_id, task_id, sample_index)
        REFERENCES evaluation_question_results (tenant_id, task_id, sample_index) ON DELETE CASCADE,
    CONSTRAINT evaluation_human_ratings_supersedes_fk FOREIGN KEY (supersedes_id)
        REFERENCES evaluation_human_ratings (id) ON DELETE RESTRICT,
    CONSTRAINT evaluation_human_ratings_revision_check CHECK (revision > 0),
    CONSTRAINT evaluation_human_ratings_score_check CHECK (score BETWEEN 1 AND 5),
    CONSTRAINT evaluation_human_ratings_rubric_check CHECK (octet_length(rubric_snapshot::text) <= 16384),
    CONSTRAINT evaluation_human_ratings_comment_check CHECK (octet_length(comment) <= 4000),
    CONSTRAINT evaluation_human_ratings_unique UNIQUE (tenant_id, task_id, sample_index, revision),
    CONSTRAINT evaluation_human_ratings_supersedes_unique UNIQUE (supersedes_id)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_human_ratings_question
    ON evaluation_human_ratings (tenant_id, task_id, sample_index, revision DESC);

CREATE INDEX IF NOT EXISTS idx_evaluation_human_ratings_rubric
    ON evaluation_human_ratings (tenant_id, task_id, sample_index, rubric_key, rubric_version, revision DESC);

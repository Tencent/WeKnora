CREATE TABLE IF NOT EXISTS evaluation_human_ratings (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    sample_index INTEGER NOT NULL,
    revision INTEGER NOT NULL,
    rater_id TEXT NOT NULL,
    rubric_key TEXT NOT NULL,
    rubric_version TEXT NOT NULL,
    rubric_snapshot TEXT NOT NULL CHECK (json_valid(rubric_snapshot)),
    score INTEGER NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    supersedes_id TEXT,
    created_at DATETIME NOT NULL,
    CONSTRAINT evaluation_human_ratings_question_fk
        FOREIGN KEY (tenant_id, task_id, sample_index)
        REFERENCES evaluation_question_results (tenant_id, task_id, sample_index) ON DELETE CASCADE,
    CONSTRAINT evaluation_human_ratings_supersedes_fk FOREIGN KEY (supersedes_id)
        REFERENCES evaluation_human_ratings (id) ON DELETE RESTRICT,
    CONSTRAINT evaluation_human_ratings_revision_check CHECK (revision > 0),
    CONSTRAINT evaluation_human_ratings_score_check CHECK (score BETWEEN 1 AND 5),
    CONSTRAINT evaluation_human_ratings_rubric_check CHECK (length(CAST(rubric_snapshot AS BLOB)) <= 16384),
    CONSTRAINT evaluation_human_ratings_comment_check CHECK (length(CAST(comment AS BLOB)) <= 4000),
    CONSTRAINT evaluation_human_ratings_unique UNIQUE (tenant_id, task_id, sample_index, revision),
    CONSTRAINT evaluation_human_ratings_supersedes_unique UNIQUE (supersedes_id)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_human_ratings_question
    ON evaluation_human_ratings (tenant_id, task_id, sample_index, revision DESC);

CREATE INDEX IF NOT EXISTS idx_evaluation_human_ratings_rubric
    ON evaluation_human_ratings (tenant_id, task_id, sample_index, rubric_key, rubric_version, revision DESC);

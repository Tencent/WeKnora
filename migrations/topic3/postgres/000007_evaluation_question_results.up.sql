-- Per-question evaluation results with transactional publication.
-- Reserved parallel-draft migration number for M3 slice 4 (see docs/evaluation-m2-m5-target-architecture.md section 10).

-- The composite foreign key below requires a unique constraint over
-- (tenant_id, id); the primary key already guarantees it transitively.
ALTER TABLE evaluation_tasks
    ADD CONSTRAINT evaluation_tasks_tenant_id_id_uk UNIQUE (tenant_id, id);

CREATE TABLE IF NOT EXISTS evaluation_question_results (
    tenant_id            BIGINT NOT NULL,
    task_id              VARCHAR(128) NOT NULL,
    sample_index         INT NOT NULL,
    qid                  VARCHAR(128) NOT NULL,
    question             TEXT NOT NULL,
    reference_answer     TEXT NOT NULL DEFAULT '',
    ground_truth_pids    JSONB NOT NULL DEFAULT '[]'::JSONB,
    search_results       JSONB NOT NULL DEFAULT '[]'::JSONB,
    rerank_results       JSONB NOT NULL DEFAULT '[]'::JSONB,
    generation_pids      JSONB NOT NULL DEFAULT '[]'::JSONB,
    generated_text       TEXT NOT NULL DEFAULT '',
    error_code           VARCHAR(64) NOT NULL DEFAULT '',
    per_sample_metrics   JSONB NOT NULL DEFAULT '{}'::JSONB,
    metric_observations  JSONB NOT NULL DEFAULT '[]'::JSONB,
    retrieval_ms         BIGINT,
    rerank_ms            BIGINT,
    generation_ms        BIGINT,
    total_ms             BIGINT,
    prompt_tokens        INT,
    completion_tokens    INT,
    total_tokens         INT,
    status               VARCHAR(16) NOT NULL,
    result_hash          CHAR(64) NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, task_id, sample_index),
    CONSTRAINT evaluation_question_results_task_fk
        FOREIGN KEY (tenant_id, task_id)
        REFERENCES evaluation_tasks (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT evaluation_question_results_sample_check CHECK (sample_index >= 0),
    CONSTRAINT evaluation_question_results_status_check
        CHECK (status IN ('success', 'failed', 'canceled'))
);

COMMENT ON TABLE evaluation_question_results IS
    'Immutable per-question evaluation facts: rankings with provenance placeholders, generation, per-sample metrics.';
COMMENT ON COLUMN evaluation_question_results.search_results IS
    'Raw retrieval order: [{rank, pid, score, provenance}]; unknown or duplicate sources keep pid=-1.';
COMMENT ON COLUMN evaluation_question_results.result_hash IS
    'Canonical SHA-256 of the row content; identical retries are idempotent, differing hashes conflict.';

CREATE INDEX IF NOT EXISTS idx_evaluation_question_results_task_page
    ON evaluation_question_results (tenant_id, task_id, sample_index)
    WHERE deleted_at IS NULL;

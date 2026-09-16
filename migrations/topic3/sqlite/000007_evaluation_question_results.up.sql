-- Mirrors versioned migration 000096_evaluation_question_results.

CREATE UNIQUE INDEX IF NOT EXISTS evaluation_tasks_tenant_id_id_uk
    ON evaluation_tasks (tenant_id, id);

CREATE TABLE IF NOT EXISTS evaluation_question_results (
    tenant_id            INTEGER NOT NULL,
    task_id              TEXT NOT NULL,
    sample_index         INTEGER NOT NULL,
    qid                  TEXT NOT NULL,
    question             TEXT NOT NULL,
    reference_answer     TEXT NOT NULL DEFAULT '',
    ground_truth_pids    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(ground_truth_pids)),
    search_results       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(search_results)),
    rerank_results       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(rerank_results)),
    generation_pids      TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(generation_pids)),
    generated_text       TEXT NOT NULL DEFAULT '',
    error_code           TEXT NOT NULL DEFAULT '',
    per_sample_metrics   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(per_sample_metrics)),
    metric_observations  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(metric_observations)),
    retrieval_ms         INTEGER,
    rerank_ms            INTEGER,
    generation_ms        INTEGER,
    total_ms             INTEGER,
    prompt_tokens        INTEGER,
    completion_tokens    INTEGER,
    total_tokens         INTEGER,
    status               TEXT NOT NULL,
    result_hash          TEXT NOT NULL CHECK (length(result_hash) = 64),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at           DATETIME,
    PRIMARY KEY (tenant_id, task_id, sample_index),
    CONSTRAINT evaluation_question_results_task_fk
        FOREIGN KEY (tenant_id, task_id)
        REFERENCES evaluation_tasks (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT evaluation_question_results_sample_check CHECK (sample_index >= 0),
    CONSTRAINT evaluation_question_results_status_check
        CHECK (status IN ('success', 'failed', 'canceled'))
);

CREATE INDEX IF NOT EXISTS idx_evaluation_question_results_task_page
    ON evaluation_question_results (tenant_id, task_id, sample_index)
    WHERE deleted_at IS NULL;

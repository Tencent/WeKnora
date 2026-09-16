-- Mirrors versioned migration 000094_evaluation_datasets.

CREATE TABLE IF NOT EXISTS evaluation_datasets (
    id                 TEXT PRIMARY KEY,
    scope              TEXT NOT NULL,
    owner_tenant_id    INTEGER,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    current_version_id TEXT,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT evaluation_datasets_scope_check
        CHECK (scope IN ('system', 'tenant')),
    CONSTRAINT evaluation_datasets_scope_tenant_check
        CHECK ((scope = 'system') = (owner_tenant_id IS NULL))
);

CREATE TABLE IF NOT EXISTS evaluation_dataset_versions (
    id               TEXT PRIMARY KEY,
    dataset_id       TEXT NOT NULL REFERENCES evaluation_datasets (id) ON DELETE RESTRICT,
    version_number   INTEGER NOT NULL,
    schema_version   INTEGER NOT NULL,
    artifact_sha256  TEXT NOT NULL CHECK (length(artifact_sha256) = 64),
    content_sha256   TEXT NOT NULL CHECK (length(content_sha256) = 64),
    manifest         TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(manifest)),
    passage_count    INTEGER NOT NULL,
    question_count   INTEGER NOT NULL,
    relevance_count  INTEGER NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT evaluation_dataset_versions_number_uk UNIQUE (dataset_id, version_number),
    CONSTRAINT evaluation_dataset_versions_content_uk UNIQUE (dataset_id, content_sha256),
    CONSTRAINT evaluation_dataset_versions_counts_check
        CHECK (passage_count >= 0 AND question_count >= 0 AND relevance_count >= 0)
);

CREATE TABLE IF NOT EXISTS evaluation_dataset_passages (
    dataset_version_id TEXT NOT NULL REFERENCES evaluation_dataset_versions (id) ON DELETE RESTRICT,
    pid                TEXT NOT NULL,
    content            TEXT NOT NULL,
    metadata           TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
    PRIMARY KEY (dataset_version_id, pid)
);

CREATE TABLE IF NOT EXISTS evaluation_dataset_questions (
    dataset_version_id TEXT NOT NULL REFERENCES evaluation_dataset_versions (id) ON DELETE RESTRICT,
    qid                TEXT NOT NULL,
    sample_index       INTEGER NOT NULL,
    question           TEXT NOT NULL,
    answer             TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (dataset_version_id, qid),
    CONSTRAINT evaluation_dataset_questions_sample_uk UNIQUE (dataset_version_id, sample_index),
    CONSTRAINT evaluation_dataset_questions_sample_check CHECK (sample_index >= 0)
);

CREATE TABLE IF NOT EXISTS evaluation_dataset_relevance (
    dataset_version_id TEXT NOT NULL,
    qid                TEXT NOT NULL,
    pid                TEXT NOT NULL,
    grade              INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (dataset_version_id, qid, pid),
    CONSTRAINT evaluation_dataset_relevance_question_fk
        FOREIGN KEY (dataset_version_id, qid)
        REFERENCES evaluation_dataset_questions (dataset_version_id, qid) ON DELETE RESTRICT,
    CONSTRAINT evaluation_dataset_relevance_passage_fk
        FOREIGN KEY (dataset_version_id, pid)
        REFERENCES evaluation_dataset_passages (dataset_version_id, pid) ON DELETE RESTRICT,
    CONSTRAINT evaluation_dataset_relevance_grade_check CHECK (grade >= 0)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_datasets_tenant
    ON evaluation_datasets (owner_tenant_id)
    WHERE owner_tenant_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_evaluation_dataset_versions_dataset
    ON evaluation_dataset_versions (dataset_id, version_number DESC);

CREATE INDEX IF NOT EXISTS idx_evaluation_dataset_questions_version_sample
    ON evaluation_dataset_questions (dataset_version_id, sample_index);

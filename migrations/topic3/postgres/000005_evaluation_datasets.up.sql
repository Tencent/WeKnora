-- Evaluation dataset registry: immutable dataset versions with canonical content hashes.
-- Reserved parallel-draft migration number for M3 slice 1 (see docs/evaluation-m2-m5-target-architecture.md section 10).

CREATE TABLE IF NOT EXISTS evaluation_datasets (
    id                 VARCHAR(64) PRIMARY KEY,
    scope              VARCHAR(16) NOT NULL,
    owner_tenant_id    BIGINT,
    name               VARCHAR(255) NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    current_version_id VARCHAR(64),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT evaluation_datasets_scope_check
        CHECK (scope IN ('system', 'tenant')),
    CONSTRAINT evaluation_datasets_scope_tenant_check
        CHECK ((scope = 'system') = (owner_tenant_id IS NULL))
);

COMMENT ON TABLE evaluation_datasets IS
    'Evaluation dataset identities. System datasets are readable by every tenant; tenant datasets stay isolated.';
COMMENT ON COLUMN evaluation_datasets.current_version_id IS
    'Globally unique dataset_version_id of the version new tasks default to.';

CREATE TABLE IF NOT EXISTS evaluation_dataset_versions (
    id               VARCHAR(64) PRIMARY KEY,
    dataset_id       VARCHAR(64) NOT NULL REFERENCES evaluation_datasets (id) ON DELETE RESTRICT,
    version_number   INT NOT NULL,
    schema_version   INT NOT NULL,
    artifact_sha256  CHAR(64) NOT NULL,
    content_sha256   CHAR(64) NOT NULL,
    manifest         JSONB NOT NULL DEFAULT '{}'::JSONB,
    passage_count    INT NOT NULL,
    question_count   INT NOT NULL,
    relevance_count  INT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT evaluation_dataset_versions_number_uk UNIQUE (dataset_id, version_number),
    CONSTRAINT evaluation_dataset_versions_content_uk UNIQUE (dataset_id, content_sha256),
    CONSTRAINT evaluation_dataset_versions_counts_check
        CHECK (passage_count >= 0 AND question_count >= 0 AND relevance_count >= 0)
);

COMMENT ON TABLE evaluation_dataset_versions IS
    'Immutable evaluation dataset versions. id is the globally unique dataset_version_id used by APIs and tasks.';
COMMENT ON COLUMN evaluation_dataset_versions.version_number IS
    'Human-readable per-dataset sequence; never used as a global identifier.';
COMMENT ON COLUMN evaluation_dataset_versions.artifact_sha256 IS
    'SHA-256 of the imported artifact (bundle file or canonical request payload).';
COMMENT ON COLUMN evaluation_dataset_versions.content_sha256 IS
    'Canonical schema-version-1 content SHA-256 over sorted passages, questions, and relevance rows.';

CREATE TABLE IF NOT EXISTS evaluation_dataset_passages (
    dataset_version_id VARCHAR(64) NOT NULL REFERENCES evaluation_dataset_versions (id) ON DELETE RESTRICT,
    pid                VARCHAR(128) NOT NULL,
    content            TEXT NOT NULL,
    metadata           JSONB NOT NULL DEFAULT '{}'::JSONB,
    PRIMARY KEY (dataset_version_id, pid)
);

CREATE TABLE IF NOT EXISTS evaluation_dataset_questions (
    dataset_version_id VARCHAR(64) NOT NULL REFERENCES evaluation_dataset_versions (id) ON DELETE RESTRICT,
    qid                VARCHAR(128) NOT NULL,
    sample_index       INT NOT NULL,
    question           TEXT NOT NULL,
    answer             TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (dataset_version_id, qid),
    CONSTRAINT evaluation_dataset_questions_sample_uk UNIQUE (dataset_version_id, sample_index),
    CONSTRAINT evaluation_dataset_questions_sample_check CHECK (sample_index >= 0)
);

CREATE TABLE IF NOT EXISTS evaluation_dataset_relevance (
    dataset_version_id VARCHAR(64) NOT NULL,
    qid                VARCHAR(128) NOT NULL,
    pid                VARCHAR(128) NOT NULL,
    grade              INT NOT NULL DEFAULT 1,
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

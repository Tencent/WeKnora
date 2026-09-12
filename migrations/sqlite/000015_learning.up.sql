CREATE TABLE learning_profiles (
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(128) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    epoch BIGINT NOT NULL DEFAULT 1 CHECK (epoch > 0),
    PRIMARY KEY (tenant_id, subject_id)
);
CREATE TABLE learning_mastery (
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(128) NOT NULL,
    page_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    p_mastery DOUBLE PRECISION NOT NULL DEFAULT 0.2 CHECK (p_mastery BETWEEN 0 AND 1),
    attempts INTEGER NOT NULL DEFAULT 0,
    correct INTEGER NOT NULL DEFAULT 0,
    consecutive_correct INTEGER NOT NULL DEFAULT 0,
    last_assessed_at DATETIME,
    next_review_at DATETIME,
    viewed_at DATETIME,
    source_stamp VARCHAR(64) NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, subject_id, page_id),
    FOREIGN KEY (tenant_id, subject_id) REFERENCES learning_profiles (tenant_id, subject_id)
);
CREATE INDEX idx_learning_mastery_kb ON learning_mastery (knowledge_base_id);
CREATE INDEX idx_learning_mastery_due ON learning_mastery (tenant_id, subject_id, next_review_at);
CREATE TABLE learning_quizzes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(128) NOT NULL,
    page_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    epoch BIGINT NOT NULL,
    source_stamp VARCHAR(64) NOT NULL,
    source_knowledge_ids TEXT NOT NULL DEFAULT '[]',
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending','running','ready','failed','stale')),
    error_code VARCHAR(32) NOT NULL DEFAULT '',
    lease_token VARCHAR(36) NOT NULL DEFAULT '',
    lease_until DATETIME,
    claims INTEGER NOT NULL DEFAULT 0,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_version VARCHAR(32) NOT NULL DEFAULT 'learning-quiz-v1',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (tenant_id, subject_id) REFERENCES learning_profiles (tenant_id, subject_id)
);
CREATE INDEX idx_learning_quiz_scope ON learning_quizzes (tenant_id, subject_id, page_id);
CREATE INDEX idx_learning_quiz_kb ON learning_quizzes (knowledge_base_id);
CREATE INDEX idx_learning_quiz_recovery ON learning_quizzes (status, lease_until, updated_at);
CREATE UNIQUE INDEX idx_learning_quiz_active ON learning_quizzes (tenant_id, subject_id, page_id) WHERE status IN ('pending','running');
CREATE TABLE learning_questions (
    id VARCHAR(36) PRIMARY KEY,
    quiz_id VARCHAR(36) NOT NULL REFERENCES learning_quizzes(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    prompt TEXT NOT NULL,
    options TEXT NOT NULL,
    correct_option TEXT NOT NULL,
    explanation TEXT NOT NULL,
    evidence TEXT NOT NULL,
    fingerprint VARCHAR(64) NOT NULL,
    UNIQUE (quiz_id, position)
);
CREATE INDEX idx_learning_question_quiz ON learning_questions (quiz_id);
CREATE TABLE learning_attempts (
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(128) NOT NULL,
    attempt_id VARCHAR(64) NOT NULL,
    question_id VARCHAR(36) NOT NULL REFERENCES learning_questions(id) ON DELETE CASCADE,
    quiz_id VARCHAR(36) NOT NULL,
    page_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    fingerprint VARCHAR(64) NOT NULL,
    source_stamp VARCHAR(64) NOT NULL,
    before DOUBLE PRECISION NOT NULL,
    after DOUBLE PRECISION NOT NULL,
    credited BOOLEAN NOT NULL,
    algorithm_version VARCHAR(32) NOT NULL,
    result TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, subject_id, attempt_id),
    FOREIGN KEY (tenant_id, subject_id) REFERENCES learning_profiles (tenant_id, subject_id)
);
CREATE UNIQUE INDEX idx_learning_answer_once ON learning_attempts (tenant_id, subject_id, question_id);
CREATE UNIQUE INDEX idx_learning_credit_once ON learning_attempts (tenant_id, subject_id, page_id, fingerprint) WHERE credited = TRUE;
CREATE INDEX idx_learning_attempt_kb ON learning_attempts (tenant_id, subject_id, knowledge_base_id);
CREATE INDEX idx_learning_attempt_quiz ON learning_attempts (quiz_id);

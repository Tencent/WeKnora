-- MySQL 8 translation of 000055_knowledge_processing_spans.
CREATE TABLE knowledge_processing_spans (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    knowledge_id VARCHAR(64) NOT NULL,
    attempt INT NOT NULL DEFAULT 1,
    span_id VARCHAR(64) NOT NULL,
    parent_span_id VARCHAR(64),
    name VARCHAR(64) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    input JSON,
    output JSON,
    metadata JSON,
    error_code VARCHAR(64),
    error_message TEXT,
    error_detail TEXT,
    started_at TIMESTAMP NULL,
    finished_at TIMESTAMP NULL,
    duration_ms BIGINT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id)
);

CREATE INDEX idx_kpspan_knowledge_attempt ON knowledge_processing_spans (knowledge_id, attempt);
CREATE INDEX idx_kpspan_status_started ON knowledge_processing_spans (status, started_at);
CREATE INDEX idx_kpspan_parent ON knowledge_processing_spans (parent_span_id);

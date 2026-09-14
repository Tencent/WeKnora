DROP INDEX IF EXISTS idx_learning_credit_once;

CREATE UNIQUE INDEX idx_learning_credit_once
ON learning_attempts (
    tenant_id,
    subject_id,
    page_id,
    fingerprint
)
WHERE credited = TRUE;

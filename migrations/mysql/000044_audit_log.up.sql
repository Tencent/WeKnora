-- MySQL 8 translation of 000044_audit_log.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE audit_logs (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    actor_user_id   VARCHAR(36) NOT NULL DEFAULT '',
    actor_role      VARCHAR(32) NOT NULL DEFAULT '',
    action          VARCHAR(64) NOT NULL,
    target_type     VARCHAR(32) NOT NULL DEFAULT '',
    target_id       VARCHAR(64) NOT NULL DEFAULT '',
    target_user_id  VARCHAR(36) NOT NULL DEFAULT '',
    request_path    VARCHAR(512) NOT NULL DEFAULT '',
    request_method  VARCHAR(16)  NOT NULL DEFAULT '',
    outcome         VARCHAR(16)  NOT NULL DEFAULT 'success',
    details         JSON        NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_audit_logs_tenant_id_desc ON audit_logs(tenant_id, id DESC);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_user_id);
CREATE INDEX idx_audit_logs_tenant_action ON audit_logs(tenant_id, action);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);

-- Workspace outbound knowledge event webhooks (SQLite / lite mode).
-- Mirrors migrations/versioned/000080_tenant_webhooks.up.sql without JSONB /
-- BIGSERIAL / partial indexes.

CREATE TABLE IF NOT EXISTS tenant_webhook_endpoints (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   INTEGER NOT NULL,
    name        VARCHAR(64) NOT NULL DEFAULT '',
    url         VARCHAR(512) NOT NULL,
    secret_enc  TEXT NOT NULL DEFAULT '',
    events      TEXT NOT NULL DEFAULT '[]',
    enabled     INTEGER NOT NULL DEFAULT 1,
    description VARCHAR(256) NOT NULL DEFAULT '',
    created_by  VARCHAR(36) NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  DATETIME
);

CREATE INDEX IF NOT EXISTS idx_webhook_ep_tenant
    ON tenant_webhook_endpoints (tenant_id);

CREATE TABLE IF NOT EXISTS tenant_webhook_deliveries (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id  VARCHAR(64) NOT NULL,
    endpoint_id  VARCHAR(36) NOT NULL,
    tenant_id    INTEGER NOT NULL,
    event_id     VARCHAR(64) NOT NULL,
    event_type   VARCHAR(64) NOT NULL,
    status       VARCHAR(16) NOT NULL,
    http_status  INTEGER NOT NULL DEFAULT 0,
    attempts     INTEGER NOT NULL DEFAULT 0,
    error        VARCHAR(512) NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  DATETIME
);

CREATE INDEX IF NOT EXISTS idx_webhook_dlv_endpoint_id
    ON tenant_webhook_deliveries (endpoint_id, id DESC);

CREATE INDEX IF NOT EXISTS idx_webhook_dlv_tenant_created
    ON tenant_webhook_deliveries (tenant_id, created_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_dlv_delivery_id
    ON tenant_webhook_deliveries (delivery_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_dlv_event_endpoint
    ON tenant_webhook_deliveries (event_id, endpoint_id);

CREATE INDEX IF NOT EXISTS idx_webhook_dlv_event_id
    ON tenant_webhook_deliveries (event_id);

CREATE TABLE IF NOT EXISTS tenant_webhook_outbox (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        VARCHAR(64) NOT NULL,
    event_type      VARCHAR(64) NOT NULL,
    owner_tenant_id INTEGER NOT NULL,
    payload         TEXT NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      VARCHAR(512) NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    processed_at    DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_outbox_event_id
    ON tenant_webhook_outbox (event_id);

CREATE INDEX IF NOT EXISTS idx_webhook_outbox_status_created
    ON tenant_webhook_outbox (status, created_at);

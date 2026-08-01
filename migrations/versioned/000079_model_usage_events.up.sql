-- Migration: 000079_model_usage_events
-- Stores per-model usage metering events for the model usage dashboard.
-- The table deliberately does not store prompts, retrieved documents, image
-- bytes, audio bytes, or model outputs.
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating table: model_usage_events'; END $$;

CREATE TABLE IF NOT EXISTS model_usage_events (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    user_id           VARCHAR(64) NOT NULL DEFAULT '',
    request_id        VARCHAR(64) NOT NULL DEFAULT '',
    model_id          VARCHAR(64) NOT NULL DEFAULT '',
    model_name        VARCHAR(255) NOT NULL DEFAULT '',
    model_type        VARCHAR(32) NOT NULL DEFAULT '',
    model_source      VARCHAR(32) NOT NULL DEFAULT '',
    provider          VARCHAR(64) NOT NULL DEFAULT '',
    request_kind      VARCHAR(64) NOT NULL DEFAULT '',
    usage_source      VARCHAR(32) NOT NULL DEFAULT '',
    prompt_tokens     BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cached_tokens     BIGINT NOT NULL DEFAULT 0,
    total_tokens      BIGINT NOT NULL DEFAULT 0,
    input_items       INTEGER NOT NULL DEFAULT 0,
    duration_ms       BIGINT NOT NULL DEFAULT 0,
    success           BOOLEAN NOT NULL DEFAULT TRUE,
    error_type        VARCHAR(128) NOT NULL DEFAULT '',
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_model_usage_events_tenant_time
    ON model_usage_events (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_model_usage_events_tenant_model_time
    ON model_usage_events (tenant_id, model_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_model_usage_events_tenant_type_time
    ON model_usage_events (tenant_id, model_type, created_at DESC);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] model_usage_events table ready'; END $$;

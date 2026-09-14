-- Migration: 000014_wechat_oa_preauth
-- Pending WeChat OA Cloud pre-auth sessions for QR binding.
-- (align with versioned 000086)

CREATE TABLE IF NOT EXISTS wechat_oa_preauths (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    cloud_preauth_id VARCHAR(128) NOT NULL DEFAULT '',
    state VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'wait',
    qrcode_url TEXT NOT NULL DEFAULT '',
    callback_secret VARCHAR(128) NOT NULL DEFAULT '',
    channel_id VARCHAR(36) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wechat_oa_preauths_tenant_agent
    ON wechat_oa_preauths (tenant_id, agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wechat_oa_preauths_state
    ON wechat_oa_preauths (state);

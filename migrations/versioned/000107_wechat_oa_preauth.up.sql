-- Migration: 000086_wechat_oa_preauth
DO $$ BEGIN RAISE NOTICE '[Migration 000086] Creating wechat_oa_preauths'; END $$;

CREATE TABLE IF NOT EXISTS wechat_oa_preauths (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    cloud_preauth_id VARCHAR(128) NOT NULL DEFAULT '',
    state VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'wait',
    qrcode_url TEXT NOT NULL DEFAULT '',
    callback_secret VARCHAR(128) NOT NULL DEFAULT '',
    channel_id VARCHAR(36) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wechat_oa_preauths_tenant_agent
    ON wechat_oa_preauths (tenant_id, agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wechat_oa_preauths_state
    ON wechat_oa_preauths (state);

COMMENT ON TABLE wechat_oa_preauths IS 'Pending WeChat OA Cloud pre-auth sessions for QR binding';
COMMENT ON COLUMN wechat_oa_preauths.status IS 'wait|scaned|bound|expired|cancelled|failed';
COMMENT ON COLUMN wechat_oa_preauths.callback_secret IS 'HMAC secret from Cloud CreatePreAuth; verifies BindingComplete until channel exists';

DO $$ BEGIN RAISE NOTICE '[Migration 000086] Done'; END $$;

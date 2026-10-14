CREATE TABLE external_identities (
    provider VARCHAR(32) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    subject VARCHAR(128) NOT NULL,
    user_id VARCHAR(36) NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider, namespace, subject)
);
CREATE INDEX idx_external_identities_user_id ON external_identities(user_id);

CREATE TABLE we_chat_login_attempts (
    id VARCHAR(36) PRIMARY KEY,
    client VARCHAR(16) NOT NULL,
    app_id VARCHAR(128) NOT NULL,
    state_hash VARCHAR(64) NOT NULL,
    config_hash VARCHAR(64) NOT NULL DEFAULT '',
    challenge VARCHAR(43) NOT NULL,
    result TEXT NOT NULL DEFAULT '',
    expires_at DATETIME NOT NULL
);
CREATE INDEX idx_we_chat_login_attempts_expires_at ON we_chat_login_attempts(expires_at);

CREATE TABLE we_chat_auth_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version BIGINT NOT NULL,
    platform_id VARCHAR(128) NOT NULL DEFAULT '',
    web_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    web_app_id VARCHAR(128) NOT NULL DEFAULT '',
    web_app_secret TEXT NOT NULL DEFAULT '',
    web_callback_url TEXT NOT NULL DEFAULT '',
    mobile_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    mobile_app_id VARCHAR(128) NOT NULL DEFAULT '',
    mobile_app_secret TEXT NOT NULL DEFAULT '',
    updated_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE users ADD COLUMN auth_source VARCHAR(16) NOT NULL DEFAULT 'local';
UPDATE users SET auth_source = 'oidc' WHERE json_extract(preferences, '$.oidc_only_login') = 1;

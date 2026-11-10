-- Browser authorization is durable; live browser processes remain ephemeral.
CREATE TABLE browser_devices (
 scope_key VARCHAR(32) PRIMARY KEY,
 id VARCHAR(32) NOT NULL UNIQUE,
 tenant BIGINT NOT NULL,
 "user" VARCHAR(36) NOT NULL,
 label VARCHAR(100) NOT NULL,
 token_hash VARCHAR(64) NOT NULL UNIQUE,
 previous_hash VARCHAR(64) NOT NULL DEFAULT '',
 previous_until DATETIME NOT NULL,
 expires_at DATETIME NOT NULL,
 renew_after DATETIME NOT NULL,
 created_at DATETIME NOT NULL,
 last_seen_at DATETIME NOT NULL,
 revoked_at DATETIME,
 owner VARCHAR(32) NOT NULL DEFAULT '',
 owner_url VARCHAR(500) NOT NULL DEFAULT '',
 lease_key VARCHAR(32) NOT NULL DEFAULT '',
 lease_until DATETIME NOT NULL
);
CREATE TABLE browser_pairings (
 scope_key VARCHAR(32) PRIMARY KEY,
 token_hash VARCHAR(64) NOT NULL UNIQUE,
 tenant BIGINT NOT NULL,
 "user" VARCHAR(36) NOT NULL,
 expires_at DATETIME NOT NULL
);
CREATE INDEX browser_pairings_expiry ON browser_pairings(expires_at);
CREATE TABLE browser_task_interruptions (
 scope_key VARCHAR(32) NOT NULL,
 session VARCHAR(36) NOT NULL,
 PRIMARY KEY (scope_key, session)
);

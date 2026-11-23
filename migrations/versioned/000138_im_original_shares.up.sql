CREATE TABLE im_original_shares (
    channel_id VARCHAR(36) PRIMARY KEY REFERENCES im_channels(id) ON DELETE CASCADE,
    agent_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    grantor_id VARCHAR(36) NOT NULL,
    revision VARCHAR(36) NOT NULL
);

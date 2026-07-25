ALTER TABLE messages
    ADD COLUMN agent_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN agent_tenant_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN model_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN execution_context JSON NOT NULL DEFAULT (JSON_OBJECT());

CREATE INDEX idx_messages_agent_id ON messages(agent_id);

CREATE TABLE message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id BIGINT NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    allow_regenerate BOOLEAN NOT NULL DEFAULT FALSE,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions JSON NOT NULL DEFAULT (JSON_ARRAY()),
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until DATETIME(3) NULL,
    generated_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_msg_suggestion_sets_tenant
        FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_message_suggestion_sets_cache_key
    ON message_suggestion_sets(
        tenant_id,
        assistant_message_id,
        placement,
        config_hash,
        locale
    );
CREATE INDEX idx_message_suggestion_sets_session
    ON message_suggestion_sets(tenant_id, session_id, created_at);
CREATE INDEX idx_message_suggestion_sets_status
    ON message_suggestion_sets(status, lease_until);

CREATE TABLE message_suggestion_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_msg_suggestion_events_tenant
        FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    CONSTRAINT fk_msg_suggestion_events_set
        FOREIGN KEY (suggestion_set_id) REFERENCES message_suggestion_sets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_message_suggestion_events_set
    ON message_suggestion_events(suggestion_set_id, created_at);
CREATE INDEX idx_message_suggestion_events_session
    ON message_suggestion_events(tenant_id, session_id, created_at);
CREATE INDEX idx_message_suggestion_events_type
    ON message_suggestion_events(event_type, created_at);

UPDATE custom_agents
SET config = JSON_SET(
    JSON_REMOVE(config, '$.suggested_prompts'),
    '$.question_suggestions',
    COALESCE(
        JSON_EXTRACT(config, '$.question_suggestions'),
        JSON_OBJECT(
            'starters', JSON_OBJECT(
                'enabled', TRUE,
                'mode', 'hybrid',
                'items', COALESCE(JSON_EXTRACT(config, '$.suggested_prompts'), JSON_ARRAY()),
                'count', 6
            ),
            'follow_ups', JSON_OBJECT(
                'enabled', FALSE,
                'mode', 'hybrid',
                'count', 3,
                'categories', JSON_ARRAY('clarify', 'deepen', 'action'),
                'max_context_turns', 2,
                'suppress_on_fallback', TRUE,
                'suppress_when_answer_asks_question', TRUE,
                'knowledge_fallback', TRUE,
                'allow_regenerate', FALSE
            )
        )
    )
)
WHERE config IS NOT NULL;

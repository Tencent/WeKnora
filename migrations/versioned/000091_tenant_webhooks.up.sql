-- Workspace outbound knowledge event webhooks (outbox + endpoints + deliveries).

CREATE TABLE IF NOT EXISTS tenant_webhook_endpoints (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   BIGINT NOT NULL,
    name        VARCHAR(64) NOT NULL DEFAULT '',
    url         VARCHAR(512) NOT NULL,
    secret_enc  TEXT NOT NULL DEFAULT '',
    events      JSONB NOT NULL DEFAULT '[]'::JSONB,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    description VARCHAR(256) NOT NULL DEFAULT '',
    created_by  VARCHAR(36) NOT NULL DEFAULT '',
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  TIMESTAMP WITH TIME ZONE
);

COMMENT ON TABLE tenant_webhook_endpoints IS '工作空间出站回调端点。一行 = 一个 HTTPS 接收地址';
COMMENT ON COLUMN tenant_webhook_endpoints.id IS '端点 UUID，应用生成，36 字符';
COMMENT ON COLUMN tenant_webhook_endpoints.tenant_id IS '所属工作空间 tenants.id；只向该空间端点投递';
COMMENT ON COLUMN tenant_webhook_endpoints.name IS '设置页展示名；不参与 HTTP 投递';
COMMENT ON COLUMN tenant_webhook_endpoints.url IS '回调地址。生产仅 https；本地允许 loopback http';
COMMENT ON COLUMN tenant_webhook_endpoints.secret_enc IS 'HMAC-SHA256 密钥，AES-GCM 落库；API 永不回显';
COMMENT ON COLUMN tenant_webhook_endpoints.events IS '订阅的 type 数组。禁止空数组；未订阅的未来 type 不投递';
COMMENT ON COLUMN tenant_webhook_endpoints.enabled IS 'false 时跳过投递，不删配置、不删投递历史';
COMMENT ON COLUMN tenant_webhook_endpoints.description IS '可选备注';
COMMENT ON COLUMN tenant_webhook_endpoints.created_by IS '创建人 user id；系统/种子行为空串';
COMMENT ON COLUMN tenant_webhook_endpoints.created_at IS '行创建时间 UTC';
COMMENT ON COLUMN tenant_webhook_endpoints.updated_at IS '配置变更时间 UTC；投递尝试不更新此列';
COMMENT ON COLUMN tenant_webhook_endpoints.deleted_at IS '软删时间。NULL = 有效。未删除行上 (tenant_id, url) 唯一';

CREATE INDEX IF NOT EXISTS idx_webhook_ep_tenant
    ON tenant_webhook_endpoints (tenant_id)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_ep_tenant_url
    ON tenant_webhook_endpoints (tenant_id, url)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS tenant_webhook_deliveries (
    id           BIGSERIAL PRIMARY KEY,
    delivery_id  VARCHAR(64) NOT NULL,
    endpoint_id  VARCHAR(36) NOT NULL,
    tenant_id    BIGINT NOT NULL,
    event_id     VARCHAR(64) NOT NULL,
    event_type   VARCHAR(64) NOT NULL,
    status       VARCHAR(16) NOT NULL,
    http_status  INTEGER NOT NULL DEFAULT 0,
    attempts     INTEGER NOT NULL DEFAULT 0,
    error        VARCHAR(512) NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  TIMESTAMP WITH TIME ZONE
);

COMMENT ON TABLE tenant_webhook_deliveries IS '出站投递排障记录。不是队列：asynq 才是队列。不存事件 JSON body';
COMMENT ON COLUMN tenant_webhook_deliveries.delivery_id IS '本次投递 ID，对应 Header X-WeKnora-Delivery；入队时生成，asynq 重试不变';
COMMENT ON COLUMN tenant_webhook_deliveries.endpoint_id IS '入队时的 tenant_webhook_endpoints.id；端点软删后历史仍可读';
COMMENT ON COLUMN tenant_webhook_deliveries.tenant_id IS '空间 id 冗余，便于裁剪与按空间排查';
COMMENT ON COLUMN tenant_webhook_deliveries.event_id IS '领域事件 id（JSON 去重键 id）';
COMMENT ON COLUMN tenant_webhook_deliveries.event_type IS '事件 type，与 JSON type、Header X-WeKnora-Event 相同';
COMMENT ON COLUMN tenant_webhook_deliveries.status IS 'pending=在途或重试中；success=对端 2xx；failed=重试耗尽或不可重试 4xx';
COMMENT ON COLUMN tenant_webhook_deliveries.http_status IS '对端末次 HTTP 状态；超时/DNS/连不上为 0';
COMMENT ON COLUMN tenant_webhook_deliveries.attempts IS '该 delivery_id 已 POST 次数';
COMMENT ON COLUMN tenant_webhook_deliveries.error IS '末次错误或非 2xx 摘要，最多 512；成功为空串';
COMMENT ON COLUMN tenant_webhook_deliveries.duration_ms IS '末次 POST 往返毫秒';
COMMENT ON COLUMN tenant_webhook_deliveries.created_at IS '该 delivery_id 首次写入';
COMMENT ON COLUMN tenant_webhook_deliveries.finished_at IS '进入 success/failed 的时间；pending 时为 NULL';

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
    id              BIGSERIAL PRIMARY KEY,
    event_id        VARCHAR(64) NOT NULL,
    event_type      VARCHAR(64) NOT NULL,
    owner_tenant_id BIGINT NOT NULL,
    payload         JSONB NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      VARCHAR(512) NOT NULL DEFAULT '',
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    processed_at    TIMESTAMP WITH TIME ZONE
);

COMMENT ON TABLE tenant_webhook_outbox IS '领域事件持久化（at-least-once 的真源）。一行 = 一条尚未按端点展开的事件';
COMMENT ON COLUMN tenant_webhook_outbox.event_id IS '包络 id，全局唯一';
COMMENT ON COLUMN tenant_webhook_outbox.event_type IS '包络 type';
COMMENT ON COLUMN tenant_webhook_outbox.owner_tenant_id IS '资源所属工作空间；Dispatch 只查该空间端点';
COMMENT ON COLUMN tenant_webhook_outbox.payload IS '不含 download.ticket 的 canonical JSON';
COMMENT ON COLUMN tenant_webhook_outbox.status IS 'pending=尚未交给 asynq；processed=已入队（不是对端 2xx）；failed=扫表超过上限';
COMMENT ON COLUMN tenant_webhook_outbox.attempts IS 'sweep/dispatch 尝试次数';
COMMENT ON COLUMN tenant_webhook_outbox.last_error IS '末次入队错误摘要';
COMMENT ON COLUMN tenant_webhook_outbox.processed_at IS '标 processed 的时间；pending 时为 NULL';

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_outbox_event_id
    ON tenant_webhook_outbox (event_id);

CREATE INDEX IF NOT EXISTS idx_webhook_outbox_status_created
    ON tenant_webhook_outbox (status, created_at)
    WHERE status = 'pending';

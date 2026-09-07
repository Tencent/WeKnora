# 工作空间知识事件回调

把本空间**自己拥有的**知识库、知识、成员变化，推到 Owner 配置的 HTTPS 地址。这和「发布集成 → 网页嵌入」里的聊天 Webhook 不是同一套：这里是知识生命周期，那边是会话消息。

配置路径：设置 → 空间 → **事件回调**（仅 Owner）。每个空间最多 5 个 URL；生产须 https。密钥用于 HMAC，创建后不再回显。设置页折叠区有验签代码和各事件的 JSON 载荷示例。

## 投递什么

P0 事件：

- `knowledge.created` / `parse_completed` / `parse_failed` / `deleted` / `batch_deleted`
- `kb.created` / `kb.deleted`（删库只发这一条，不刷 N 条文档事件）
- `rbac.member_added` / `rbac.member_removed`（本工作空间用户加入/移除，不是共享空间）
- `webhook.test`（设置页点测试）

组织共享的 `kb.share_*` 和扇出到成员空间是后续能力，本版不做。全量底账用现有列表 API（`GET /knowledge-bases`、`GET /knowledge-bases/:id/knowledge`），不要拿 Webhook 当全量同步。

包络字段固定：`spec_version` / `id` / `type` / `time` / `tenant_id` / `actor_user_id` / `request_id` / `data`。按 `type` 分支；`data.resource` 标明种类（`knowledge` | `knowledge_base` | `member` | `webhook`），同一 `resource` 下 `data` 的 key 集合固定，空值也会出现。

```text
event = JSON.parse(raw_body)
switch event.type:
  knowledge.created         → upsert 目录; maybe download
  knowledge.parse_completed → upsert 可检索（可早于 created）；看 enable_status
  knowledge.parse_failed    → 标记失败
  knowledge.deleted         → 按 knowledge_id 删除
  knowledge.batch_deleted   → 按 knowledge_ids 删除
  kb.created                → 建库节点
  kb.deleted                → 按 knowledge_base_id 删除库及子文档
  rbac.member_added         → 授权用户
  rbac.member_removed       → 收回用户
  webhook.test              → 记成功，忽略 data
```

## 怎么验签

WeKnora 作为客户端 POST 到你填的 URL：

```http
POST /hooks/weknora HTTP/1.1
Content-Type: application/json
User-Agent: WeKnora-Workspace-Webhook/1.0
X-WeKnora-Event: knowledge.created
X-WeKnora-Delivery: dlv_...
X-WeKnora-Timestamp: 1756624860
X-WeKnora-Signature: sha256=...
```

签名是 `HMAC-SHA256(secret, timestamp + "." + raw_body)`，**不是**只签 body（嵌入渠道聊天 Webhook 才是只签 body）。必须用原始字节验签，不要先 parse JSON 再序列化。按包络 `id` 去重；投递至少一次。`X-WeKnora-Delivery` 只用于日志（同一次 asynq 重试不变），业务去重用 body.`id`。

## 源文件下载票

`knowledge.created` / `parse_completed` 里若有源文件，`data.download.ticket` 是 5 分钟票：

```http
GET /api/v1/files/knowledge-download/:id
X-WeKnora-Download-Ticket: wdt1....
```

过期后 1 小时内可 `POST /api/v1/files/knowledge-download/:id/renew` 换新票。这与现网 `GET /api/v1/knowledge/:id/download`（需要 Contributor+）是两条路，不要把知识 API 加进匿名白名单。

`url` / `passage` 等非文件：`download.available=false`，`reason=not_a_file`；URL 用 `data.source`。删除类事件：`reason=deleted`，不要再去 download。`kb.deleted` **没有** `data.download`。

图片与其它渠道的文件访问见[图片与文件的对外访问](21-file-access.md)。

## 事件 JSON 示例

下面每条都是接收方实际读到的 **body**。示例中的 `ticket` 已脱敏；真实票只出现在回调里，收到后马上用来 GET `path`。P0 包络 `tenant_id` 与知识事件的 `data.owner_tenant_id` 相同。

### `knowledge.created`（文件，可下载）

```json
{
  "spec_version": "1",
  "id": "evt_01K3N8G2R8X4Y6Z8A0B1C2D3E4",
  "type": "knowledge.created",
  "time": "2026-08-31T06:01:00Z",
  "tenant_id": 42,
  "actor_user_id": "usr_8f2a",
  "request_id": "req_ab12",
  "data": {
    "resource": "knowledge",
    "knowledge_id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
    "knowledge_base_id": "b7e1a2c0-1111-2222-3333-444444444444",
    "owner_tenant_id": 42,
    "title": "example.pdf",
    "knowledge_type": "file",
    "source": "",
    "file_type": "pdf",
    "parse_status": "pending",
    "enable_status": "disabled",
    "folder_path": "guides",
    "error_message": "",
    "deleted": false,
    "knowledge_ids": [],
    "count": 0,
    "total_count": 0,
    "batch_index": 0,
    "batch_total": 0,
    "delete_batch_id": "",
    "truncated": false,
    "download": {
      "available": true,
      "reason": "",
      "http_method": "GET",
      "path": "/api/v1/files/knowledge-download/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
      "ticket": "wdt1.eyJwdXJwb3NlIjoia25vd2xlZGdlX2Rvd25sb2FkIiwi...redacted.sig",
      "ticket_expires_at": "2026-08-31T06:06:00Z",
      "ticket_header": "X-WeKnora-Download-Ticket"
    }
  }
}
```

此时目录应占位，**不可检索**。尽快用票拉源文件。URL 型 `knowledge_type=url` 时 `download.available=false`、`reason=not_a_file`，用 `data.source`。

### `knowledge.parse_completed`

与 created 同一套 `data` key。差异：`parse_status=completed`，`enable_status=enabled`（流水线打开检索，不是用户点了启用）。`actor_user_id` / `request_id` 常为空串（worker 完成，没有终端用户）。即使还没收到 `created` 也可以 upsert；已是 `completed` 的行不要打回 `pending`。

### `knowledge.parse_failed`

`parse_status=failed`，`error_message` 有原因。源文件通常仍在，仍可 download 排障。

### `knowledge.deleted`

`deleted=true`，`download.reason=deleted`。按 `knowledge_id` 删本地副本，不要再 download。

### `knowledge.batch_deleted`

不为每条 id 再发 `knowledge.deleted`。`knowledge_ids` 最多 100 条，超出则同一次删除再发下一条（`delete_batch_id` 相同，`batch_index` / `batch_total` / `total_count` 对账）。`truncated` 恒为 `false`。跨库先按库拆开再分片。

```json
{
  "spec_version": "1",
  "id": "evt_01K3N8M...",
  "type": "knowledge.batch_deleted",
  "time": "2026-08-31T06:11:00Z",
  "tenant_id": 42,
  "actor_user_id": "usr_8f2a",
  "request_id": "req_gh78",
  "data": {
    "resource": "knowledge",
    "knowledge_id": "",
    "knowledge_base_id": "b7e1a2c0-1111-2222-3333-444444444444",
    "owner_tenant_id": 42,
    "title": "",
    "knowledge_type": "",
    "source": "",
    "file_type": "",
    "parse_status": "",
    "enable_status": "",
    "folder_path": "",
    "error_message": "",
    "deleted": true,
    "knowledge_ids": [
      "11111111-1111-1111-1111-111111111111",
      "22222222-2222-2222-2222-222222222222"
    ],
    "count": 2,
    "total_count": 2,
    "batch_index": 1,
    "batch_total": 1,
    "delete_batch_id": "bdel_01K3N8M0SAMEBATCH0000000001",
    "truncated": false,
    "download": {
      "available": false,
      "reason": "deleted",
      "http_method": "GET",
      "path": "",
      "ticket": "",
      "ticket_expires_at": "",
      "ticket_header": "X-WeKnora-Download-Ticket"
    }
  }
}
```

### `kb.created`

```json
{
  "spec_version": "1",
  "id": "evt_01K3N8N...",
  "type": "kb.created",
  "time": "2026-08-31T06:00:00Z",
  "tenant_id": 42,
  "actor_user_id": "usr_8f2a",
  "request_id": "req_ij90",
  "data": {
    "resource": "knowledge_base",
    "knowledge_base_id": "b7e1a2c0-1111-2222-3333-444444444444",
    "name": "Sample KB",
    "visibility": "private",
    "cascade_knowledge": false,
    "knowledge_count": 0,
    "unavailable_to_tenant": false,
    "share_id": "",
    "source_tenant_id": 0
  }
}
```

### `kb.deleted`

整库对本空间不可用。`cascade_knowledge=true`，`unavailable_to_tenant=true`。接收方按 `knowledge_base_id` 删库及子文档，**不要**再等 N 条 `knowledge.deleted`，也不要 download。此事件没有 `data.download`。

```json
{
  "spec_version": "1",
  "id": "evt_01K3N8P...",
  "type": "kb.deleted",
  "time": "2026-08-31T07:00:00Z",
  "tenant_id": 42,
  "actor_user_id": "usr_8f2a",
  "request_id": "req_kl12",
  "data": {
    "resource": "knowledge_base",
    "knowledge_base_id": "b7e1a2c0-1111-2222-3333-444444444444",
    "name": "Sample KB",
    "visibility": "private",
    "cascade_knowledge": true,
    "knowledge_count": 128,
    "unavailable_to_tenant": true,
    "share_id": "",
    "source_tenant_id": 0
  }
}
```

### `rbac.member_added` / `rbac.member_removed`

成员是「设置 → 空间 → 成员」里的用户，不是共享空间/组织名册。移除时 `reason` 为 `removed`（踢出）或 `left`（自己离开）。该用户对本空间全部知识失去访问；库和文档还在。

```json
{
  "spec_version": "1",
  "id": "evt_01K3N8R...",
  "type": "rbac.member_added",
  "time": "2026-08-31T08:00:00Z",
  "tenant_id": 42,
  "actor_user_id": "usr_owner",
  "request_id": "req_op56",
  "data": {
    "resource": "member",
    "user_id": "usr_guest",
    "role": "viewer",
    "reason": "added",
    "email": "guest@example.com"
  }
}
```

### `webhook.test`

设置页「发送测试」发出。`data` 只有 `resource` + `ok`，不要用知识字段去解。

```json
{
  "spec_version": "1",
  "id": "evt_01K3N8S...",
  "type": "webhook.test",
  "time": "2026-08-31T08:30:00Z",
  "tenant_id": 42,
  "actor_user_id": "usr_owner",
  "request_id": "",
  "data": {
    "resource": "webhook",
    "ok": true
  }
}
```

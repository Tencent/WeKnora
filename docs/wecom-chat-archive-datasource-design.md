# WeKnora 企业微信会话存档数据源设计

## 背景与目标

为 WeKnora 新增原生数据源连接器 `wecom_chat_archive`，从企业微信「会话内容存档」API 拉取聊天记录，并复用 WeKnora 现有数据源导入框架同步到指定知识库。

该连接器第一版聚焦聊天文本归档，不处理复杂附件解析，不新增外部同步服务。它复用现有能力：数据源配置、凭证加密、手动同步、Cron 定时调度、asynq 异步任务、同步日志、增量游标和知识入库管道。

## MVP 范围

第一版支持：

- 源端为企业微信会话内容存档 API。
- WeKnora 形态为原生 Connector。
- 同步范围为企业微信会话内容存档授权范围内的全量会话。
- 首次或强制全量同步默认回溯最近 90 天。
- 增量同步基于企业微信全局 `seq` 游标。
- 文档粒度为 `conversation_id + yyyy-mm-dd`。
- 同步文本、Markdown、链接、图文摘要、mixed 中可转文本部分。
- 图片、语音、视频、文件等附件只记录元数据占位。
- 撤回消息只记录事件，不删除 WeKnora 知识。
- 保存真实姓名、userid、external userid、room id。
- 在知识 metadata 中记录聊天参与者集合，为后续文档级权限过滤准备。

第一版不支持：

- 不下载或解析图片、语音、视频、文件附件。
- 不做 OCR 或 ASR。
- 不做会话白名单、部门过滤、成员过滤。
- 不做撤回消息驱动的 WeKnora 删除。
- 不做消息级或文档级权限过滤执行。
- 不做话题或 thread 自动切分。
- 不做实时 webhook，同步由 Cron 或手动触发。

## 部署与 SDK 约束

生产实现使用企业微信官方会话存档 SDK/CGO，MVP 仅承诺 Linux amd64 生产目标。

SDK 细节必须封装在内部 `ArchiveClient` 边界后面，连接器主体不直接依赖 CGO 细节。单元测试使用 fake `ArchiveClient`，避免测试依赖真实企业微信账号、会话存档权限或本机 SDK 环境。

Docker 镜像需要在后续实现计划中明确官方 SDK 动态库、头文件、运行时库路径和 Linux amd64 构建方式。

SDK 文件准备、本地 Linux amd64 CGO 构建、Docker 打包和运行时排查步骤见 `docs/wecom-chat-archive-sdk.md`。

## 连接器类型与元数据

在 `internal/types/datasource.go` 新增：

```go
const ConnectorTypeWeComChatArchive = "wecom_chat_archive"
```

在 `internal/datasource/connector.go` 新增 metadata：

```go
types.ConnectorTypeWeComChatArchive: {
    Type:         types.ConnectorTypeWeComChatArchive,
    Name:         "企业微信会话存档",
    Description:  "从企业微信会话内容存档同步聊天记录到知识库",
    Icon:         "wecom",
    Priority:     2,
    AuthType:     "api_key",
    Capabilities: []string{"incremental"},
}
```

第一版不声明 `deletion_sync`，因为撤回或删除事件不驱动 WeKnora 删除知识，只在聚合内容中记录。

## 文件结构

新增目录：

```text
internal/datasource/connector/wecom_chat_archive/
├── types.go
├── client.go
├── client_linux_amd64.go
├── connector.go
├── markdown.go
└── connector_test.go
```

职责：

- `types.go`：配置、游标、企业微信 API/SDK 消息类型、内部标准消息类型、聚合桶。
- `client.go`：定义 `ArchiveClient` 接口、token cache、脱敏错误工具、fake 注入点。
- `client_linux_amd64.go`：官方 SDK/CGO 实现，仅 Linux amd64 编译。
- `connector.go`：实现 WeKnora `datasource.Connector`。
- `markdown.go`：消息转 Markdown、聚合桶渲染、sender 展示。
- `connector_test.go`：fake client 单测，不依赖真实企业微信。

需要修改：

- `internal/types/datasource.go`：新增 connector type 常量。
- `internal/datasource/connector.go`：注册 ConnectorMetadata。
- `internal/container/container.go`：`initConnectorRegistry` 注册新 connector。
- `frontend/src/views/knowledge/settings/DataSourceEditorDialog.vue`：增加企业微信会话存档类型和表单字段。
- 前端 i18n 文件：增加显示名称、字段名、提示文案。

## 配置结构

数据源解密后的配置示例：

```json
{
  "type": "wecom_chat_archive",
  "credentials": {
    "corp_id": "wwxxxx",
    "secret": "xxxx",
    "private_key": "-----BEGIN PRIVATE KEY-----...",
    "private_key_version": "1"
  },
  "resource_ids": ["all"],
  "settings": {
    "sync_scope": "all_archived_conversations",
    "aggregation": "conversation_day",
    "timezone": "Asia/Shanghai",
    "full_sync_days": 90,
    "include_message_types": ["text", "markdown", "link", "news", "mixed"],
    "attachment_policy": "metadata_only",
    "include_sender_name": true,
    "include_sender_id": true,
    "include_room_id": true,
    "include_external_user_id": true,
    "sync_revoke_as_delete": false,
    "record_participants_for_acl": true
  }
}
```

默认值：

- `resource_ids`: `all`。
- `timezone`: `Asia/Shanghai`。
- `full_sync_days`: `90`。
- `attachment_policy`: `metadata_only`。
- `sync_revoke_as_delete`: `false`。
- `record_participants_for_acl`: `true`。

凭证由 WeKnora `data_sources.config` 加密存储。日志、错误信息、同步结果中不得输出 `secret`、`private_key` 明文。

## Connector 接口实现

当前仓库实际 `Connector` 接口包含 `parentID` 和 `ResolveResourceAncestors`，实现时必须以当前代码为准：

```go
type Connector interface {
    Type() string
    Validate(ctx context.Context, config *types.DataSourceConfig) error
    ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error)
    ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error)
    FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error)
    FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error)
}
```

### Type

```go
func (c *Connector) Type() string {
    return types.ConnectorTypeWeComChatArchive
}
```

### Validate

验证流程：

1. 校验 `corp_id`、`secret`、`private_key`、`private_key_version` 非空。
2. 使用 `corp_id + secret` 获取企业微信 `access_token`。
3. 初始化官方会话存档 SDK。
4. 加载私钥和私钥版本。
5. 调用最小读接口或拉取小批消息验证权限和解密链路。
6. 对错误脱敏后返回。

Validate 不落库明文凭证，也不输出私钥、secret、消息密钥或聊天正文。

### ListResources

第一版资源选择简化为单个虚拟资源。`parentID == ""` 返回 `all`，`parentID != ""` 返回空数组。

```go
func (c *Connector) ListResources(
    ctx context.Context,
    config *types.DataSourceConfig,
    parentID string,
) ([]types.Resource, error) {
    if parentID != "" {
        return []types.Resource{}, nil
    }
    return []types.Resource{{
        ExternalID:  "all",
        Name:        "全部已授权会话",
        Type:        "wecom_chat_archive_scope",
        Description: "同步企业微信会话内容存档授权范围内的所有会话",
        Metadata: map[string]interface{}{
            "scope": "all_archived_conversations",
        },
    }}, nil
}
```

后续如果支持会话白名单、成员过滤、部门过滤，可以扩展 `ListResources`，但不进入 MVP。

### ResolveResourceAncestors

WeCom MVP 是扁平单虚拟资源，无需祖先解析。

```go
func (c *Connector) ResolveResourceAncestors(
    ctx context.Context,
    config *types.DataSourceConfig,
    resourceIDs []string,
) ([]string, error) {
    return []string{}, nil
}
```

### FetchAll

全量同步用于首次导入或强制重建。第一版全量不是企业全历史，而是最近 90 天。

流程：

1. 从配置读取 `full_sync_days`，默认 `90`。
2. 计算起始时间 `now - full_sync_days`。
3. 从企业微信归档接口按 `seq` 顺序拉取消息。
4. 解密消息体。
5. 丢弃早于起始时间的消息。
6. 过滤和转换 MVP 支持的消息类型。
7. 按 `conversation_id + yyyy-mm-dd` 聚合消息。
8. 将每个聚合桶渲染为 Markdown，并返回 `FetchedItem` 列表。

企业微信归档 API 是 seq 流，不应在设计中承诺可以直接按时间倒查 90 天。实现需要按 seq 扫描并按消息时间过滤，同时设置分页、批次上限、超时保护和同步日志提示，避免无限扫描。

### FetchIncremental

增量同步基于企业微信会话内容存档的全局 `seq` 游标。

```go
type weComChatArchiveCursor struct {
    LastSeq      uint64            `json:"last_seq"`
    LastMsgTime  int64             `json:"last_msg_time"`
    LastSyncTime string            `json:"last_sync_time"`
    DayBuckets   map[string]string `json:"day_buckets,omitempty"`
}
```

该结构存入 `types.SyncCursor.ConnectorCursor`。

流程：

1. 解析上次 cursor。
2. 从 `LastSeq + 1` 开始分批拉取消息。
3. 解密消息体。
4. 过滤支持的消息类型。
5. 按 `conversation_id + yyyy-mm-dd` 聚合。
6. 将聚合结果转换为 `FetchedItem`。
7. 返回新 cursor，记录最大 `seq`、最大消息时间、本轮同步完成时间和可选 day bucket 摘要。

如果某条消息解密或转换失败，记录到错误摘要或同步日志，整体同步继续。只有认证失败、拉取 API 连续失败、私钥错误、SDK 初始化失败等系统性错误才使同步任务失败。系统性失败不得推进 cursor。

## ArchiveClient 边界

连接器依赖接口，不直接依赖 SDK：

```go
type ArchiveClient interface {
    Validate(ctx context.Context) error
    FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error)
    Close() error
}
```

统一解密后消息结构：

```go
type ArchiveMessageEnvelope struct {
    Seq              uint64
    MsgID            string
    Action           string
    MsgType          string
    ConversationID   string
    ConversationName string
    ConversationType string
    RoomID           string
    From             Sender
    ToList           []Sender
    MsgTime          time.Time
    Raw              json.RawMessage
}

type Sender struct {
    UserID         string
    ExternalUserID string
    Name           string
    Type           string // internal|external|bot|unknown
}
```

`client_linux_amd64.go` 负责官方 SDK/CGO 调用、access token 刷新、SDK 初始化、拉取、解密、限流重试和 SDK 资源释放。

## 文档聚合规则

聚合 key：

```text
conversation_id + yyyy-mm-dd(settings.timezone)
```

日期按 `settings.timezone` 计算，默认 `Asia/Shanghai`。

聚合桶维护：

- 消息列表。
- 首条和末条消息时间。
- 最大 `seq`。
- 参与者 userids。
- 参与者 external userids。
- 发送者 userids。
- 发送者 external userids。
- room ids。
- 转换失败计数。
- 附件占位计数。
- 撤回事件计数。

`FetchedItem` 示例：

```go
types.FetchedItem{
    ExternalID:       "wecom-chat:{conversation_id}:{yyyy-mm-dd}",
    Title:            "企业微信会话 {conversation_name} {yyyy-mm-dd}",
    Content:          []byte(renderedMarkdown),
    ContentType:      "text/markdown",
    FileName:         "wecom-chat-{conversation_id}-{yyyy-mm-dd}.md",
    URL:              "wecom://chat/{conversation_id}?date={yyyy-mm-dd}",
    UpdatedAt:        lastMessageTime,
    SourceResourceID: "all",
    Metadata: map[string]string{
        "source": "wecom_chat_archive",
        "conversation_id": "...",
        "conversation_type": "room|single|unknown",
        "date": "2026-07-07",
        "message_count": "128",
        "first_msg_time": "...",
        "last_msg_time": "...",
        "last_seq": "...",
        "participant_userids": "zhangsan,lisi",
        "participant_external_userids": "wm_xxx",
        "participant_room_ids": "wr_xxx",
        "participant_count": "3",
        "sender_userids": "zhangsan,lisi",
        "sender_external_userids": "wm_xxx",
        "sender_policy": "real_name_and_userid",
    },
}
```

`ExternalID` 必须稳定。重复同步同一天同一会话时，WeKnora 现有入库逻辑可按 `external_id` 找到旧知识并覆盖重建。

参与者相关字段需要去重并稳定排序，方便后续权限过滤和测试断言。

## Markdown 渲染格式

建议不把完整参与者列表写入正文，只放 metadata，避免参与者列表进入向量检索内容。

```markdown
# 企业微信会话：客户项目群 / 2026-07-07

> conversation_id: wr_xxx
> conversation_type: room
> message_count: 128
> time_range: 09:01:22 - 18:43:10

## 消息记录

[09:01:22] 张三（zhangsan）:
今天客户反馈了一个问题，需要确认日志。

[09:05:12] 李四（lisi）:
我看一下。

[09:06:30] 王五（wangwu）:
[链接] 问题单：xxx
https://example.com/ticket/123

[09:10:03] 外部联系人（external_userid: wm_xxx）:
[附件: image, msgid=xxx, 未解析]

[09:12:00] 张三（zhangsan）:
[消息已撤回, msgid=xxx]
```

发送人展示规则：

- 内部成员：`真实姓名（userid）`。
- 外部联系人：如果能解析姓名则 `真实姓名（external_userid: xxx）`，否则 `外部联系人（external_userid: xxx）`。
- 机器人或未知发送人：保留原始 sender id，并标记 `unknown_sender`。

## 消息类型策略

MVP 支持正文入库：

- `text`: 正文入库。
- `markdown`: Markdown 文本入库。
- `link`: 标题、描述、URL 入库。
- `news`: 标题、摘要、URL 入库。
- `mixed`: 拆出其中可转文本部分；不可转部分写占位。

MVP 仅元数据占位：

- `image`: `[附件: image, msgid=..., 未解析]`
- `voice`: `[附件: voice, msgid=..., 未转写]`
- `video`: `[附件: video, msgid=..., 未解析]`
- `file`: `[附件: file, filename=..., msgid=..., 未解析]`
- 其他未知类型：`[未支持消息类型: type, msgid=...]`

撤回策略：

- 不删除 WeKnora 已有知识。
- 在后续同步中记录撤回事件：`[消息已撤回, msgid=...]`。
- 不设置 `FetchedItem.IsDeleted`。
- `sync_deletions` 默认关闭。

## 权限扩展设计

MVP 只记录权限判定所需数据，不修改 WeKnora 当前权限模型。

当前 WeKnora 的知识权限主要是知识库级 RBAC 和共享访问。会话存档连接器先将聊天参与者写入 `Knowledge.Metadata`，后续可在检索、读取或知识列表层增加文档级过滤。

稳定 metadata 字段：

- `participant_userids`
- `participant_external_userids`
- `sender_userids`
- `sender_external_userids`
- `participant_room_ids`
- `participant_count`

后续权限过滤建议：

- 用户账号绑定企业微信 `userid`。
- 查询、检索、读取知识时增加 document-level filter。
- 普通用户只能看到 `participant_userids` 包含自己的聊天文档。
- KB owner/admin 可绕过文档级参与者过滤。
- 外部联系人默认不开放，除非有单独身份映射。

安全提示：MVP 不执行文档级 ACL。如果目标知识库对无关用户开放，他们仍可能看到聊天文档。因此该数据源应绑定到受限知识库，不应和普通知识库混放。

## 定时同步

使用 WeKnora 现有 `sync_schedule` 字段和 Cron 调度器。

推荐默认：

- 稳定同步：`0 */30 * * * *`，每 30 分钟。
- 高频同步：`0 */10 * * * *`，每 10 分钟。
- 低频合规归档：`0 0 * * * *`，每小时。

同步沿用现有流程：

```text
Cron/手动触发 -> 创建 SyncLog -> asynq 入队 -> Worker ProcessSync -> Connector Fetch -> ingestItem -> 更新 cursor/log
```

保留 WeKnora 现有两层去重：

- DB 层 `HasRunningSync` 防止同一数据源并发同步。
- 队列层 `TaskID` 防止同一分钟多实例重复入队。

## 前端配置向导

创建数据源时的表单：

1. 选择类型：企业微信会话存档。
2. 填写凭证：
   - 企业 ID `corp_id`
   - Secret `secret`
   - 私钥版本 `private_key_version`
   - 私钥 `private_key`
3. 测试连接：调用 `ValidateCredentials`。
4. 选择资源：显示单个资源「全部已授权会话」，默认选中 `all`。
5. 同步策略：
   - 同步模式：增量同步默认。
   - 首次全量范围：最近 90 天默认，可配置。
   - 同步频率：每 10 分钟、每 30 分钟、每小时、自定义 Cron。
   - 冲突策略：覆盖推荐。
   - 同步删除：默认关闭。

字段行为：

- `secret` 标记为 secret。
- `private_key` 标记为 secret，并使用多行输入。
- 默认 `sync_deletions = false`。
- 默认 `sync_schedule = "0 */30 * * * *"`。

提示文案：

- 会话存档需在企业微信后台完成合规授权。
- 附件仅记录占位，不解析内容。
- 聊天参与者 userid 会写入文档 metadata，用于后续权限过滤。

## 错误处理

系统性失败会终止本轮同步并不推进 cursor：

- 凭证错误。
- `access_token` 获取失败。
- 官方 SDK 初始化失败。
- 私钥错误。
- 企业微信权限不足。
- 拉取 API 连续失败超过重试次数。

单条消息失败不中断整体同步：

- 单条消息解密失败。
- 单条消息类型转换失败。
- 附件类型不支持。
- 单条消息字段缺失但可跳过。

单个 conversation-day 渲染失败时，该桶计入 failed，不影响其他桶。

企业微信 API 限流按 SDK/HTTP 返回做指数退避，超过重试次数后同步失败。

## 脱敏与合规

所有错误输出必须脱敏：

- 不输出 `secret`。
- 不输出 `private_key`。
- 不输出消息解密密钥。
- 不输出聊天正文全文到日志。
- 同步日志只输出计数、seq 范围、conversation-day external id 和错误摘要。

合规原则：

- 只同步企业微信会话内容存档授权范围内的数据。
- WeKnora 不绕过企业微信侧合规授权。
- 凭证和私钥存储在 WeKnora 加密配置中。
- 知识内容中保留真实姓名和 userid，这是已确认需求。
- 后续如果需要按用户权限过滤，应在 WeKnora 检索、读取或知识库权限层实现。

## 测试策略

单元测试：

- `Type` 返回 `wecom_chat_archive`。
- Validate 缺字段时报错且错误脱敏。
- 配置解析和默认值。
- `ListResources` 在 root 返回 `all`，非 root 返回空。
- `ResolveResourceAncestors` 返回空。
- fake `ArchiveClient` 覆盖 Validate 成功/失败。
- 文本、Markdown、链接、图文、mixed、附件、撤回、未知类型转换为 Markdown。
- 按 `conversation_id + 日期` 聚合。
- `ExternalID` 稳定。
- `UpdatedAt` 取桶内最后消息时间。
- 参与者和发送者 metadata 去重排序。
- FetchAll 正确过滤最近 90 天。
- FetchIncremental 从 `LastSeq + 1` 开始并推进 `LastSeq`。
- 单条消息失败不影响其他消息。
- 系统性失败不推进 cursor。

集成测试：

- 使用 mock adapter 模拟分页、限流、token 过期。
- 如 CI 可获得官方 SDK，使用固定密钥和样例密文验证解密链路。
- 用测试 data source 触发手动同步，确认 WeKnora 创建或更新知识、metadata 写入、cursor 推进。

手动验证：

- 创建数据源并测试连接。
- 执行最近 90 天全量同步。
- 查看 sync log 计数、seq 范围和失败摘要。
- 在知识库中搜索某条聊天文本。
- 查看知识 metadata 中参与者字段。
- 等待一次 Cron 增量同步，确认 cursor 推进。

## 风险与后续

主要风险：

- 官方 SDK/CGO 增加镜像构建复杂度。
- 90 天全量依赖 seq 流扫描，可能成本较高。
- MVP 不执行文档级 ACL，知识库授权范围必须谨慎配置。
- 聊天正文合规敏感，日志和错误处理必须严格脱敏。

后续可扩展：

- 文档级权限过滤。
- 会话白名单、成员过滤、部门过滤。
- 附件下载、OCR、ASR。
- 更细粒度的资源选择。
- webhook 或准实时同步。

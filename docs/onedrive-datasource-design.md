# OneDrive 数据源接入设计

## 1. 目标与范围

本文设计一个可向上游提交并按完整功能验收的 OneDrive 数据源 MVP。目标是：

- 支持 Microsoft 个人账号和 Microsoft Entra 工作/学校账号的 OneDrive。
- 同时支持未启用 MFA 的普通账号，以及启用了 Microsoft Authenticator、短信验证码或条件访问 MFA 的账号。
- 支持选择整个 OneDrive、文件夹或单个文件同步到知识库。
- 支持全量同步、增量同步、源文件删除同步和 OAuth 重新授权。
- 不向 WeKnora 暴露或保存 Microsoft 账号密码、Authenticator 动态验证码。

MVP 暂不包含：

- SharePoint 站点和文档库浏览；数据模型需要为后续支持保留 `drive_id`。
- 应用权限（client credentials）无人值守同步。
- Webhook 推送、共享给我的文件、快捷方式和 OneDrive 国家云。

后续可以在不改变知识入库模型的情况下增加 SharePoint、应用权限和 Webhook。

### 1.1 完整贡献边界

本文中的“支持”表示后端、前端、迁移、部署文档、自动测试和人工验收均完成，不接受只注册 connector 元数据、只打通 OAuth、只支持首次全量同步或用删除计数代替实际删除的半成品状态。

以下能力是同一贡献的合并门槛：

- OAuth 创建、授权、刷新、重新授权、断开连接和数据源删除形成完整生命周期。
- 整个 drive、文件夹和单文件三种选择均支持全量、可靠增量、移动和真实删除。
- 任何文件抓取、解析或入库失败都不能因 cursor 前进而永久漏同步。
- 多租户鉴权、token 加密、日志脱敏、并发刷新和多实例部署约束有自动测试覆盖。
- 后端 API、任务状态、前端状态和用户可见错误使用一致的结构化语义。

若实现阶段无法完成独立成员索引、真实删除或可靠 cursor 提交，应显式缩小 MVP 为“整个 OneDrive 同步”，而不是宣称支持任意文件夹/文件的可靠增量同步。

## 2. 认证方案

### 2.1 推荐方案：委托式 OAuth 授权码流程

需要明确区分 OAuth 与 MFA：

- **OAuth 授权是必需的**：Microsoft Graph 读取私有 OneDrive 内容必须取得 access token。
- **Authenticator/MFA 是可选的**：是否出现动态码、推送确认或其他二次验证，由 Microsoft 账号和租户策略决定。
- WeKnora 使用同一个 OAuth 流程覆盖两种账号，不增加 `mfa_enabled` 配置，也不要求用户预先声明是否开启 Authenticator。

使用 Microsoft identity platform 的 OAuth 2.0 Authorization Code Flow，并启用 PKCE：

1. 管理员在 WeKnora 中创建一个暂停状态的 OneDrive 数据源。
2. 前端请求授权 URL，并在新窗口跳转到 Microsoft 登录页。
3. Microsoft 根据账号策略完成登录：未启用 MFA 时直接完成登录；启用 MFA 时继续完成 Authenticator 推送、六位动态码或其他二次验证。
4. Microsoft 将一次性 authorization code 回调到 WeKnora 后端。
5. 后端校验单次 `state`、PKCE `code_verifier` 和数据源归属，然后交换 access token 与 refresh token。本实现不消费 ID token，因此不申请 OpenID scope，也不依赖未校验的 JWT claim。
6. 后端调用 `GET /me/drive` 验证授权，将数据源标记为已授权。
7. 定时同步在 access token 过期前通过 refresh token 静默刷新。

建议 scope：

```text
offline_access Files.Read
```

`Files.Read` 是读取当前登录用户 OneDrive 的最小委托权限。除非扩展到 SharePoint、共享资源或跨用户读取，不应在 MVP 中申请 `Files.Read.All`、`Sites.Read.All` 或任何写权限。

Microsoft 官方资料：

- [OAuth 2.0 authorization code flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow)
- [Microsoft Graph delegated access](https://learn.microsoft.com/en-us/graph/auth-v2-user)
- [Microsoft Graph permissions reference](https://learn.microsoft.com/en-us/graph/permissions-reference)

### 2.2 Authenticator / MFA 场景

Authenticator/MFA 不是 OneDrive 数据源的必填项，也不应成为独立认证模式。未启用 MFA 的用户在 Microsoft 页面完成普通登录后即可授权；启用 MFA 的用户会由 Microsoft 自动追加二次验证。

WeKnora 不增加“账号密码”“动态验证码”“是否开启 MFA”输入框，也不实现 ROPC 密码模式。所有登录步骤完全发生在 Microsoft 域名的交互式页面中，因此同一实现可兼容：

- 未启用 MFA 的账号密码或无密码登录；
- Microsoft Authenticator 推送确认；
- Authenticator 六位 TOTP；
- 短信、电话、FIDO2 和无密码登录；
- Entra Conditional Access 要求的 MFA。

后台同步通常不需要重复输入验证码。以下情况可能使 refresh token 失效或要求再次交互：

- 用户或管理员撤销授权；
- 账号禁用、密码重置或租户策略变化；
- Conditional Access 的登录频率、设备合规或风险策略要求重新登录；
- Microsoft 返回 `invalid_grant`、`interaction_required` 或 claims challenge。

发生这些情况时应停止无意义重试，将数据源置为 `error` 或 `reauthorization_required`，在 UI 显示“重新授权”按钮。不得要求用户把验证码复制到 WeKnora。

Device Code Flow 不作为自动回退方案。企业条件访问可以专门阻止该流程，而且它比已有 Web UI 的授权码流程更容易产生钓鱼风险。只有未来针对纯命令行部署时，才考虑显式、可配置地支持。

### 2.3 应用注册

建议由每个 WeKnora 部署配置自己的 Microsoft Entra App Registration：

```text
ONEDRIVE_CLIENT_ID=
ONEDRIVE_CLIENT_SECRET=
ONEDRIVE_TENANT=common
ONEDRIVE_REDIRECT_URL=https://weknora.example.com/api/v1/datasource-oauth/onedrive/callback
```

- `common` 可覆盖个人账号及工作/学校账号，但 App Registration 必须启用对应的 supported account types。
- 企业可设置具体 tenant ID，限制只能由该租户授权。
- redirect URL 必须来自服务端配置，不能信任前端传入的任意绝对 URL。
- client secret 属于部署级秘密，不随数据源 API 返回。PKCE 仍应启用，作为 authorization code 被截获时的额外保护。

面向部署管理员和最终用户的操作、恢复与排障说明见 [Microsoft OneDrive 数据源](onedrive-datasource.md)。

## 3. Token 所有权与存储

OneDrive 数据源会被定时任务和空间内其他管理员操作，因此 token 应属于“数据源”，而不是仅属于发起授权的 Web 用户。保存 `authorized_by_user_id` 只用于审计。

不建议复用 `mcp_oauth_tokens`。MCP token 是每个 principal 隔离的交互式身份，而数据源 token 是后台任务共享的连接身份，生命周期和权限语义不同。

新增表：

```text
data_source_oauth_tokens
  id
  tenant_id
  data_source_id       UNIQUE
  provider             # onedrive
  access_token         # AES-256-GCM
  refresh_token        # AES-256-GCM
  token_type
  scopes
  expires_at
  provider_account_id
  provider_tenant_id
  authorized_drive_id
  account_display_name
  authorized_by_user_id
  connection_version    # 每次替换连接递增，隔离旧任务
  created_at
  updated_at
```

要求：

- access token、refresh token 必须使用 AES-256-GCM 加密。`SYSTEM_AES_KEY` 缺失、长度错误或无法解密时必须拒绝授权/刷新并给出配置错误，禁止降级为明文存储。
- API 响应、日志、审计详情和错误信息中禁止输出 token、authorization code、client secret、PKCE verifier 和预签名下载 URL。
- refresh token 轮换后必须原子替换旧 token。Microsoft 返回新 refresh token 时不能继续长期使用旧值。
- 多实例并发刷新应使用数据库行锁或乐观版本控制，避免两个请求同时轮换同一个 refresh token。
- 删除数据源或点击“断开连接”时删除本地 token；当前产品策略同时清空选择并精确删除该数据源同步的旧知识，UI 在执行前明确提示。UI 还应提示用户可在 Microsoft 账号/企业应用页面撤销服务端授权。
- token 写入、数据源授权状态更新和 `connection_version` 更新必须处于同一数据库事务；旧版本同步任务在写知识或 cursor 前必须失败退出。

### 3.1 重新授权与替换连接

“重新授权”默认只恢复同一个 Microsoft 连接。callback 完成在线验证后，必须比较已保存和新授权的 `provider_account_id`、可可靠取得时的 `provider_tenant_id` 与 `drive_id`。当前最小 scope 实现以 Graph 返回的稳定 account ID + drive ID 作为连接身份，不解析未验证的 access-token claim：

- 三者一致：更新 token，保留资源选择、成员索引和 cursor。
- 任一不一致：普通重新授权拒绝覆盖，并提示用户使用“替换连接”。
- 替换连接：需要二次确认；暂停调度，递增 `connection_version`，清空资源选择、cursor 和成员索引，并按产品确认的策略删除或保留旧知识。默认应删除该数据源产生的旧知识，避免把两个账号的数据混在同一数据源中。

授权弹窗启动时记录当前 `connection_version`，callback 只允许写回同一版本，防止两个管理员同时授权时后完成的旧流程覆盖新连接。

## 4. OAuth API 与状态机

新增 API：

```text
POST   /api/v1/datasource/:id/oauth/authorize-url
GET    /api/v1/datasource/:id/oauth/status
DELETE /api/v1/datasource/:id/oauth/token
GET    /api/v1/datasource-oauth/onedrive/callback
```

- 前三个接口要求当前空间 Admin+，并校验数据源属于当前 tenant。
- callback 不携带 WeKnora bearer token，依赖不可预测、单次使用、有 TTL 的 `state` 完成鉴权。
- `state` 服务端保存 10 分钟，Redis 可用时写 Redis；仅明确的 Lite 单实例模式允许内存存储。非 Lite 或多实例部署没有共享 state store 时应启动失败，不能随机让 callback 因落到另一实例而失败。
- `state` 内容包括 `tenant_id`、`data_source_id`、`authorized_by_user_id`、`connection_version`、PKCE verifier 和是否显式替换连接。redirect URI 固定取服务端环境配置，callback 只返回静态关闭页，因此不保存前端 redirect，也不存在开放重定向入口。
- callback 使用 Redis `GETDEL` 或等价原子操作，防止重放。
- 前端返回地址只允许站内相对路径或配置的 allowlist，避免开放重定向。
- callback 必须区分用户取消、state 过期/丢失、Microsoft 拒绝授权、token 交换失败和账号不匹配，返回可供 UI 映射的结构化错误码。

新增 `reauthorization_required` 状态，不与一般连接器错误混用：

```json
{
  "code": "oauth_reauthorization_required",
  "message": "Microsoft authorization expired; reconnect this data source"
}
```

创建流程需要允许 OAuth 数据源先保存为 `paused`，再进行授权。当前 `CreateDataSource` 会立即调用连接器 `Validate`，因此应引入可选的 OAuth 连接器能力，而不是在 service 中硬编码 `onedrive`：

```go
type OAuthConnector interface {
    Connector
    ValidateStaticConfig(config *types.DataSourceConfig) error
    OAuthProvider() string
}
```

普通连接器保持原行为；OAuth 连接器创建暂停记录时只校验静态配置，授权完成后再执行在线 `Validate`。未授权和需要重新授权的数据源不得恢复调度；收到不可恢复的 OAuth 错误后移除或禁用已有调度，避免重试风暴。

## 5. 运行时 token 注入

现有 `Connector` 方法只接收 `DataSourceConfig`，无法获知 data source ID，也无法安全持久化刷新后的 token。不应把 refresh token 放入 `SyncCursor`。此外，现有数据源 API 会返回完整 cursor，而 OneDrive `delta_link` 含不透明 query token；本贡献必须把 connector cursor 改为仅服务端可见，API 最多返回 `has_cursor`、cursor 版本和更新时间等非敏感摘要。

建议给 `DataSourceConfig` 增加不参与 JSON 序列化的运行时上下文：

```go
type DataSourceRuntime struct {
    DataSourceID string
    TenantID     uint64
    ConnectionVersion uint64
    AccessToken  func(context.Context) (string, error)
}

type DataSourceConfig struct {
    Type        string                 `json:"type"`
    Credentials map[string]interface{} `json:"credentials"`
    ResourceIDs []string               `json:"resource_ids"`
    Settings    map[string]interface{} `json:"settings"`
    Runtime     *DataSourceRuntime     `json:"-"`
}
```

`DataSourceService` 在 Validate、ListResources、ResolveResourceAncestors、FetchAll 和 FetchIncremental 之前统一注入 runtime token provider。token provider 负责：

- access token 距过期不足 5 分钟时刷新；
- 原子保存轮换后的 refresh token；
- 对单次 Graph 401 最多刷新并重试一次；
- 将需要交互式登录的错误转换为 `ErrOAuthReauthorizationRequired`；
- 在刷新、抓取、入库和提交 cursor 前验证 `connection_version` 未变化。

这样可以统一已有连接器的运行时调用方式，也不会把持久化职责塞进 OneDrive HTTP client。

### 5.1 统一抓取结果与 cursor 提交协议

现有 `FetchAll` 不能返回 cursor，无法实现“全量遍历前取得 delta token，遍历后追平并提交”的协议。完整实现应统一全量和增量返回值，例如：

```go
type FetchResult struct {
    Items       []types.FetchedItem
    NextCursor  *types.SyncCursor
    Warnings    []types.FetchWarning
}

FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) (*FetchResult, error)
FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) (*FetchResult, error)
```

cursor 是消费进度，不能仅以“Graph 请求成功”为提交条件。`DataSourceService` 必须遵守：

1. connector 返回 items 和候选 `NextCursor`，但不直接持久化 cursor。
2. 对每个 item 完成真实删除或知识入库；失败项进入持久化的 pending/retry 队列，或令整个批次失败。
3. 只有本批所有必须处理的 item 成功，或失败项已经可靠持久化并保证重试后，才原子提交 `NextCursor`。
4. fetch error、部分解析失败、部分入库失败和进程退出都不能提交一个会跳过未处理变更的 cursor。
5. 重放同一批次必须幂等；同一个 `ExternalID` 的更新、删除重复执行不会产生重复知识或错误删除。

不得沿用当前“fetch 失败也保存 cursor”或“只要不是全部 item 失败就保存 cursor”的通用行为到 OneDrive。若采用 pending 队列方案，PR 必须同时包含其表结构、重试策略、幂等键、清理策略和可观测性。

## 6. OneDrive 连接器结构

建议目录：

```text
internal/datasource/connector/onedrive/
  types.go
  client.go
  auth.go
  connector.go
  cursor.go
  client_test.go
  auth_test.go
  connector_test.go
```

`client.go` 只封装 Microsoft Graph v1.0：

- `GET /me/drive`
- `GET /drives/{drive-id}/root/children`
- `GET /drives/{drive-id}/items/{item-id}/children`
- `GET /drives/{drive-id}/items/{item-id}/content`
- `GET /drives/{drive-id}/root/delta`

所有列表都必须跟随 `@odata.nextLink`。下载接口会返回 302 到短期有效的预认证地址，HTTP client 应允许受控重定向，但不得记录 `Location` 或 `@microsoft.graph.downloadUrl`。

Microsoft Graph 资料：

- [DriveItem resource](https://learn.microsoft.com/en-us/graph/api/resources/driveitem?view=graph-rest-1.0)
- [List folder children](https://learn.microsoft.com/en-us/graph/api/driveitem-list-children?view=graph-rest-1.0)
- [Download DriveItem content](https://learn.microsoft.com/en-us/graph/api/driveitem-get-content?view=graph-rest-1.0)
- [DriveItem delta](https://learn.microsoft.com/en-us/graph/api/driveitem-delta?view=graph-rest-1.0)

资源 ID 使用稳定的 drive ID 与 item ID 组合，例如 URL-safe base64 编码的：

```json
{"drive_id":"...","item_id":"..."}
```

不要只使用路径：文件重命名或移动时路径会变化，而 item ID 更稳定。`FetchedItem.ExternalID` 同样使用 drive ID + item ID，避免未来接入 SharePoint 多文档库时碰撞。

资源树：

- 根调用返回当前 OneDrive 作为 drive 节点。
- 展开 drive 返回根目录 children。
- 展开 folder 返回直接 children。
- folder 设置 `has_children=true`；文件可以直接选择。
- 同时选择父文件夹和子项时先做归一化，避免重复下载和重复入库。

文件转换：

- folder 不入库，只递归遍历。
- 普通文件使用 `/content` 下载原始字节，交给现有文档解析流水线。
- 使用 OneDrive 原始文件名、Graph MIME type、lastModifiedDateTime 和 webUrl 构造 `FetchedItem`。
- 应用现有文件大小与扩展名限制；不支持的文件记录为可见 warning，而不是令整批同步失败。
- 下载使用有限并发，并沿用数据源 HTTP client 的 SSRF、超时和响应体大小保护。302 后访问预认证 URL 时不得携带 Graph `Authorization` header，并再次执行协议、目标地址、重定向次数和响应大小检查。

## 7. 全量与增量同步

Graph delta 是 drive 级变更流，文件夹/单文件选择是 WeKnora 在该变更流上的本地投影。由于 delta 中 `parentReference` 不保证包含完整 path，且文件夹重命名或移动不会保证所有后代都重新出现在 delta 中，不能只靠当前批次响应判断一个 item 是否位于选中子树。

因此 MVP 直接增加独立成员索引，不把目录状态塞进会通过 API 返回的 `SyncCursor`：

```text
data_source_items
  id
  tenant_id
  data_source_id
  connection_version
  drive_id
  item_id
  parent_item_id
  item_type             # drive/folder/file
  selected_root_id      # 归属的规范化选择根；可为空
  external_id
  last_modified_at
  last_seen_generation
  ingested
  deleted_at
  created_at
  updated_at

UNIQUE(data_source_id, connection_version, drive_id, item_id)
INDEX(data_source_id, connection_version, parent_item_id)
INDEX(data_source_id, connection_version, selected_root_id)
```

该表是同步正确性状态，不是用户凭据；所有读写必须带 `tenant_id`、`data_source_id` 和 `connection_version`。父子选择归一化后，一个 item 只归属最外层的选中根，避免重复下载和重复知识。

### 7.1 全量同步

对选中的 drive/folder/file 去重后递归遍历，下载受支持文件。为避免遍历期间发生修改而漏数据：

1. 调用 `root/delta?token=latest` 取得不枚举整个 drive 的起始 delta token。
2. 为本次扫描生成新的 generation，递归遍历规范化后的选择；同步更新 `data_source_items` 并下载受支持文件。
3. 从起始 token 消费 delta 到最新 `@odata.deltaLink`，补应用遍历期间的新增、修改、移动和删除。
4. 递归扫描完整成功且 delta 已追平时，本 generation 未见的旧成员即由本次权威扫描确认已删除、移出范围或被取消选择，产生删除操作；任何分页或子树读取失败都会终止批次，不能把不完整扫描误判为删除。
5. 完成所有知识入库/真实删除后，按照第 5.1 节的提交协议保存新 cursor。

全量同步、手动 force-full 和“选择发生变化后触发的重建”都必须返回并提交新的 delta cursor，不能继续使用旧选择对应的 cursor。

### 7.2 增量同步

cursor 保存：

```json
{
  "delta_link": "opaque Microsoft URL",
  "selection_hash": "sha256(canonical_resource_ids)",
  "connection_version": 3
}
```

- `delta_link` 必须视为不透明值，直接跟随，不自行拼接 token。
- `selection_hash` 基于排序、去重和父子归一化后的 resource IDs 计算；变化时强制全量同步并重建成员索引和 cursor。
- delta 返回 `deleted` facet 时，通过 `data_source_items` 判断其先前归属，生成真实删除操作，并将索引记录标记删除。
- delta 可能重复返回同一个 item，批次内以最后一次状态为准。
- 普通文件移动时，根据持久化 parent 链重新计算归属：移出产生删除，移入下载入库，范围内移动保持同一 `ExternalID` 并更新元数据。
- 文件夹移动或从范围外移入时，递归枚举该文件夹当前子树，并与成员索引比较；不能假设 Graph 会为所有后代生成 delta 事件。
- 文件夹收到 `deleted` facet 时，先将成员索引中的后代标为待确认；消费完整个 delta 批次并应用可能的 reparent 事件后，再对仍位于该已删除子树的知识执行删除。不能在看到文件夹事件时立即级联删除，也不能尝试枚举已经删除的远端子树。
- delta token 失效时只回退一次受控全量同步；成功后替换 cursor，失败则保留旧 cursor 并报告错误，不能无限重试旧 token。
- 收到当前 `connection_version` 之外任务的结果时全部丢弃，不写知识、成员索引或 cursor。

### 7.3 真实删除与取消选择

现有 `DataSourceService` 对 `FetchedItem{IsDeleted:true}` 只计数、不删除知识，不能满足本设计目标。完整贡献必须增加按 `(tenant_id, knowledge_base_id, datasource_id, external_id)` 精确查找并删除数据源知识的服务能力，并保证：

- 仅删除当前数据源创建的知识，不能按标题、URL 或不带数据源范围的 `external_id` 删除。
- 源文件删除、文件移出选择范围、取消选择、替换连接和删除数据源使用同一套可审计删除语义。
- `SyncDeletions=false` 时保留知识，但成员索引仍更新；UI 明确显示这是“保留源端已删除内容”，且之后重新开启删除同步能够对遗留项执行 reconcile。
- 删除失败与入库失败一样阻止 cursor 提交，或进入可靠 pending 队列。
- 重复删除是幂等成功；知识已被用户手动删除时不会令整个同步失败。

选择范围变更必须计算旧、新规范化选择的差集。被取消选择的已有知识不能静默遗留：保存前由 UI 明确提示将删除或保留多少项，并按用户选择执行；默认行为与 `SyncDeletions` 保持一致。

## 8. 错误、限流与可观测性

- 401：刷新 token 后重试一次；再次失败则要求重新授权。
- 403：区分 consent 不足、Conditional Access、文件自身无权限和租户策略阻止。
- 404：同步期间文件已删除时按删除处理，资源选择阶段则提示资源不存在。
- 429：优先遵循 `Retry-After`，否则指数退避加 jitter。
- 5xx：有限次数指数退避；禁止在单次任务内无限重试。
- 每个 Graph 请求发送 `client-request-id`，日志记录 Graph `request-id`、状态码和已脱敏 endpoint，便于排查。
- 任何日志都不记录 Authorization header、token endpoint body、预签名 URL 或完整 query token。
- 同步日志至少记录 `data_source_id`、`connection_version`、同步类型、Graph request-id、扫描/变更/入库/删除/跳过/失败数量和 cursor 是否提交；不得记录文件正文或敏感 URL。
- 用户可见 warning 必须包含稳定错误码和安全的 item 标识，不能只有自由文本，也不能因单个不支持文件将整个批次标为成功且无提示。

参考 [Microsoft Graph best practices](https://learn.microsoft.com/en-us/graph/best-practices-concept)。

## 9. 前端交互

在 `DataSourceEditorDialog.vue` 增加 OneDrive：

1. 选择 OneDrive 后显示连接状态，而不是 client secret/token 输入框。
2. “连接 Microsoft OneDrive”打开 OAuth 弹窗。
3. callback 完成后，前端轮询 `/oauth/status` 或监听弹窗关闭。
4. 显示已连接账号的脱敏名称和 tenant，不显示 token。
5. 加载资源树并选择同步范围。
6. 保存后恢复数据源定时任务。
7. token 失效时数据源卡片显示“需要重新授权”。
8. 提供“重新授权”和“断开连接”，两者都要求 Admin+。
9. 普通重新授权检测到不同账号、tenant 或 drive 时不得静默替换，改为展示“替换连接”确认以及旧知识处理方式。
10. 取消选择、断开连接、替换连接和删除数据源前，展示会受影响的已同步项目数量及删除/保留语义。

弹窗被浏览器阻止时，提供可复制/新标签页打开的授权链接。用户取消 Microsoft 登录时，保留暂停的数据源并允许重试；取消整个创建向导时删除临时数据源及 OAuth token。

## 10. 测试与完整贡献验收

后端单元测试：

- Graph 分页、文件夹递归、父子选择去重。
- 302 下载、文件大小限制、MIME/文件名传递。
- delta 新增、修改、删除、重复事件、移动和 token 失效回退。
- 文件夹重命名不返回后代、文件夹整树移入/移出、跨选中根移动、单文件选择和父子同时选择。
- 全量扫描与 delta 并发发生新增、修改、移动和删除时不漏项、不重复知识。
- 选择 hash 规范化、选择变化重建、force-full 替换 cursor 和旧 `connection_version` 任务丢弃。
- 源端删除、移出范围、取消选择、替换连接和删除数据源均执行精确、幂等的知识删除；`SyncDeletions=false` 的保留与后续 reconcile 行为正确。
- 在 fetch、下载、解析、知识写入、知识删除、成员索引写入和 cursor 写入的每个阶段注入失败，证明未处理变更不会因 cursor 前进而丢失。
- 429 `Retry-After`、5xx 退避、401 刷新一次。
- refresh token 轮换与并发刷新。
- `invalid_grant` / `interaction_required` 转换为重新授权状态。
- OAuth state 过期、重放、跨 tenant/data source 攻击和 callback open redirect。
- 两个管理员并发授权、不同账号误重新授权、显式替换连接和 callback 对旧 connection version 的竞争测试。
- token AES 加密、缺少/错误/轮换 AES key 时 fail closed、API 不返回完整 cursor、日志不泄漏。
- 无 Redis 单实例可授权；无共享 state store 的多实例配置拒绝启动或拒绝启用 OAuth。
- 数据源删除与 token、成员索引、pending 项清理的一致性和孤儿数据测试。

前端测试：

- OneDrive 类型选择和授权按钮。
- 授权成功、用户取消、弹窗阻止、token 失效重新授权。
- 资源树懒加载、编辑时恢复已有选择。
- 换账号拦截、替换连接二次确认、取消选择影响提示和真实删除结果。
- 错误状态区分需要重新授权、管理员同意、state 失效、网络错误和部分文件 warning。

人工验收矩阵：

| 账号 | MFA | 预期 |
|---|---|---|
| Microsoft 个人账号 | 未启用 | 普通 Microsoft 登录后可授权并同步 |
| Microsoft 个人账号 | Authenticator/TOTP | Microsoft 页面完成 MFA 后可同步 |
| Entra 工作账号 | 未启用 | 普通组织账号登录后可授权并同步 |
| Entra 工作账号 | Authenticator 推送 | 可授权、刷新、定时同步 |
| Entra 工作账号 | Conditional Access MFA | 策略允许时成功；要求重新交互时 UI 可恢复 |
| Entra 工作账号 | 管理员禁止用户 consent | 显示需要管理员批准，不误报密码错误 |
| 已授权账号 | 管理员撤销 consent | 下次同步进入重新授权状态，不重试风暴 |

MFA 本身不在自动测试中模拟；自动测试 mock Microsoft authorize/token endpoint，真实 MFA 通过上述人工矩阵验收。

### 10.1 端到端数据正确性验收

准备包含嵌套文件夹、同名文件、大文件、不支持格式和至少 100 个受支持文件的测试 drive，依次执行：

1. 选择整个 drive 首次同步，逐项核对知识数量、`ExternalID`、文件名、类型、更新时间和来源 URL。
2. 分别选择文件夹和单文件重复首次同步，确认范围准确且父子重叠选择不产生重复知识。
3. 新增、修改、重命名、同目录移动、移入选择范围、移出选择范围和删除文件；增量同步后逐项核对最终状态。
4. 对包含多级后代的文件夹执行重命名、移入、移出和删除，确认后代最终状态正确。
5. 在全量扫描进行中制造上述变化，确认追平 delta 后没有漏项。
6. 人工令一个文件解析或入库失败，再恢复故障，确认下一次运行最终同步成功且 cursor 未跳过该文件。
7. 撤销 consent，确认进入 `reauthorization_required` 且不重试风暴；用原账号恢复后继续增量，不做无谓全量。
8. 尝试用另一账号重新授权，确认被阻止；走显式替换连接后，旧任务失效、旧索引清理且知识处理符合确认选项。

验收以知识库最终状态与 OneDrive 选中范围一致为准，不能只依据同步日志中的计数。

### 10.2 安全与运维验收

- 数据库快照中 access token、refresh token 均为带版本前缀的密文；没有 AES key 时无法创建或授权 OneDrive 数据源。
- API、浏览器网络响应、应用日志、任务 payload 和错误追踪中不出现 token、code、PKCE verifier、完整 delta link 或预认证下载 URL。
- Admin 以下角色无法发起、查询、撤销、替换授权或浏览资源；跨 tenant 访问统一返回不可枚举的拒绝结果。
- 两实例并发同步和并发刷新不会重复轮换、覆盖新 refresh token 或提交旧 connection version 的结果。
- 429/5xx 的退避有次数与总时长上限，任务取消能够及时中止分页、下载和退避。
- 新增迁移可在项目支持的数据库上从空库和现有版本升级；约束、索引、级联清理符合预期，并提供项目惯例要求的回滚说明。

### 10.3 合并完成定义（Definition of Done）

只有同时满足以下条件，才视为完整功能贡献：

- 后端、前端、数据库迁移、配置样例、管理员部署文档和用户操作说明齐全。
- 新代码通过格式化、静态检查、单元测试、集成测试和前端测试，原有 connector 回归测试无退化。
- 所有新增外部请求都有 timeout、取消、分页上限/终止条件、限流重试和响应体大小限制。
- 所有新增持久化状态都有 tenant 隔离、唯一约束、并发策略和删除清理路径。
- UI 不展示尚未实现的 capability；connector metadata 中的 `incremental`、`deletion_sync` 等声明与实际行为一致。
- PR 描述包含权限 scope、迁移影响、配置方式、安全分析、失败恢复方式、验收证据和明确的非目标。
- 至少完成上述账号/MFA 矩阵和端到端数据正确性验收，并附关键场景的可复现记录。

实现采用“同步任务内解析终态屏障”而不是新增 pending 表：文件创建后等待知识进入 `completed`，或进入已完成主解析的 `finalizing`；`failed`、`cancelled`、状态读取失败、任务取消和 90 分钟超时都计为本批失败并阻止 cursor 前进。不支持扩展名和超限文件属于明确的非必处理项，会记录带稳定 code 与 item ID 的 partial warning，但不阻止其他已成功变更的 cursor 提交。

## 11. 建议拆分提交

为便于上游审查，建议按以下顺序提交：

1. `refactor(datasource): add reliable fetch result and cursor commit protocol`
2. `feat(datasource): add generic oauth token lifecycle`
3. `feat(datasource): add source item index and real deletion`
4. `feat(datasource): implement onedrive graph connector`
5. `feat(frontend): add onedrive oauth and resource picker`
6. `docs(datasource): document onedrive deployment, recovery and mfa`

PR 第一阶段保持 OneDrive MVP 边界，不同时加入 SharePoint 和 app-only 权限。后续 PR 再扩展：

- SharePoint site/document library + `Sites.Read.All` 或 Selected permissions；
- client credentials/certificate 的无人值守企业同步；
- Webhook + delta；
- 国家云 endpoint 配置。

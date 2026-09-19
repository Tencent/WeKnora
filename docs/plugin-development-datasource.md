# WeKnora 外部数据源插件开发指南

本文面向希望独立开发数据源插件的开发者，覆盖 Manifest、配置、gRPC Handler、同步契约、装载和验证。五类扩展点共享的生命周期与治理约定在本文中只作必要说明；其他扩展点的业务协议见第 6 节列出的独立指南。

插件应作为独立目录或独立仓库交付，由宿主从外部目录发现和装载。只要遵守本指南中的 Manifest 与 SDK 契约，插件不需要修改 WeKnora 主仓的注册表或同步流程。

## 1. 插件包结构

```text
my-plugin/
├── plugin.yaml
├── go.mod
├── main.go
└── README.md
```

`plugin.yaml` 是发现、兼容性和权限校验的入口。下面列出核心字段；除标明“必填”的字段外，其余字段按插件需要填写：

| 字段 | 说明 |
| --- | --- |
| `api_version` | **必填**，当前填写 `weknora.plugin/v1` |
| `id` | **必填**，全局稳定的小写 ID；不要包含主仓内部 DataSource ID |
| `name` | **必填**，插件在人类可见界面中的名称 |
| `extension_type` | **必填**，填写 `datasource` |
| `version` | **必填**，插件自身版本，例如 `1.0.0` |
| `protocol_version` | **必填**，当前填写 `v1` |
| `weknora_version` | 可选，宿主兼容范围，例如 `>=0.7 <1.0` |
| `entrypoint` | **必填**。进程方式填可执行文件路径（相对 `plugin.yaml` 所在目录），或 `docker://镜像:标签` 走容器运行 |
| `capabilities` | 可选，声明插件提供的能力。常用取值：`incremental`（增量同步）、`deletion_sync`（同步删除）、`streaming`（流式同步，见下节） |
| `config_schema` | 可选，用户配置项、类型、是否必填/敏感（敏感字段用 `secret: true` 标记）。字段类型支持 `string` / `string[]` / `array` / `boolean` / `integer` / `number` / `object` / `directory` / `path` / `url`（`url` 会自动做 SSRF 校验） |
| `permissions.network` | `none`（默认断网）或 `allowlist`（按白名单放行） |
| `permissions.read_paths` | 允许读取的路径声明，作为宿主审核和运行策略的输入；实际隔离仍取决于所使用的 Runtime |
| `permissions.allowed_destinations` | 当 `network` 为 `allowlist` 时使用的域名白名单 |
| `permissions.data` | 声明可处理的租户、知识库和数据源范围；`self` 表示当前调用上下文 |
| `metadata.connector_type` | 可选，自定义连接器类型名（默认用插件 `id`）。**不能与内置类型名冲突**（见 1.0.3） |
| `metadata.icon` | 可选，连接器图标。支持两种写法：相对文件名（如 `icon.png`，指向插件目录内的文件，由宿主代理读取）或绝对 `http(s)://` URL（直接透传给前端） |
| `metadata.description` | 可选，连接器描述，显示在类型选择卡片上 |
| `metadata.docs_url` | 可选，插件配置文档地址。宿主透传给前端，编辑数据源时显示“配置文档”入口 |
| `metadata.auth_type` | 可选，认证方式标识（如 `none` / `oauth2` / `api_key` / `token`），供前端展示；缺省为 `none` |
| `metadata.priority` | 可选，整数。连接器在类型列表中的展示优先级，数值越小越靠前 |

### 关于 `capabilities` 与流式同步

`capabilities` 里除了描述业务能力（`incremental` 增量同步、`deletion_sync` 删除同步），还有一个**运行时能力** `streaming`，它决定宿主用哪种方式调用插件：

- **不声明 `streaming`（默认，推荐）**：宿主走批量路径，调用 `FetchAll` / `FetchIncremental`。插件在 `FetchIncremental` 里返回的增量 cursor 会被宿主原样保存、下次同步原样传回——这是最简单、最不易出错的方式，绝大多数数据源用这个就够。
- **声明 `streaming`**：宿主走流式路径，调用 SDK 的 `OnFetchAllStream` / `OnFetchIncrementalStream` 回调，适合单次同步产生海量文档、需要边拉取边入库边 checkpoint 断点续传的场景。

约定与边界：

- `streaming` 是运行时能力，**不会**透传给前端（前端只展示 `incremental` / `deletion_sync` 这类业务能力）。
- 声明 `streaming` 的插件，**必须**同时实现 `OnFetchAllStream` 与 `OnFetchIncrementalStream` 两个回调，并且在 SDK Handler 的 `Capabilities` 里**同样回显 `streaming`**——握手时宿主会双向严格校验（manifest 声明了什么，runtime 必须逐项回显一致，否则装载失败）。
- 未声明 `streaming` 的插件**不要**实现流式回调：即使实现了，宿主也只会走批量路径，流式回调不会被调用。

一句话：**能不用流式就不用流式**。增量同步的正确性依赖 cursor 的往返，批量路径（`FetchIncremental` 返回 cursor）是最稳妥的选择；只有文档树巨大、需要断点续传时才考虑 `streaming`。

权限声明不是安全边界本身。`network: none` 的插件在容器运行时以 `--network none` 断网；开发态进程运行时不提供操作系统级网络隔离，不能作为安全隔离证明。

> 插件的实际运行隔离方式（进程 / 容器、是否断网、出口代理、Socket 权限交接）由宿主部署环境统一负责。插件作者不需要实现或配置 Runtime Agent、Docker Socket 或镜像校验，只需正确声明 `entrypoint`、`permissions` 并使用 SDK 的受控客户端。

### 1.0.1 外部数据源插件的前端元数据

除了 `config_schema` 外，数据源插件可以在 `metadata` 中声明前端展示所需的基础信息：

```yaml
metadata:
  connector_type: yourorg.example-source
  description: Synchronize documents from an external source
  icon: icon.png
  docs_url: https://example.com/plugin-setup
```

`description`、`icon` 和 `docs_url` 会随连接器元数据返回给前端。数据源编辑页面会显示插件图标、描述和配置文档链接，并统一提供可编辑的数据源**实例名称**。实例名称保存在宿主的数据源记录中，不是插件 Manifest 的字段。

凭证字段必须声明在 `config_schema.properties.credentials` 中，普通配置声明在 `config_schema.properties.settings` 中。前端只为外部插件渲染这两份动态 Schema，不会叠加内置连接器的专用凭证表单。因此，插件作者只需维护 Manifest 元数据和 Schema，不需要为每个数据源修改 WeKnora 前端。

数据范围可以随插件能力一起声明：

```yaml
permissions:
  network: none
  read_paths: ["configured-root"]
  data:
    tenants: [self]
    knowledge_bases: [self]
    data_sources: [self]
```

这些范围是宿主审核和运行策略的输入；`self` 指当前调用的租户、知识库或数据源。插件不能仅凭 gRPC metadata 扩大自己的数据权限，宿主仍负责传入数据和执行最终授权。`read_paths` 也不是插件自行获得文件系统访问权的凭证；需要文件系统隔离时，应由部署环境提供相应的挂载或沙箱策略。

### 1.0.2 自定义图标

插件可以自带连接器图标，随插件一起分发，无需把图片上传到外部 CDN：

```yaml
metadata:
  icon: icon.png          # 相对文件名 → 插件目录下的 icon.png
```

插件目录结构：

```text
my-plugin/
├── plugin.yaml           # metadata.icon: icon.png
├── my-plugin.exe
└── icon.png              # 图标文件，放在插件目录内
```

工作方式：

- **相对文件名**（如 `icon.png`）：宿主在加载插件时记录插件目录，图标通过 `GET /api/v1/datasource/icon/{connector_type}` 由宿主代理读取，前端 `<img>` 直接加载该地址。插件作者无需关心路径，只需把文件放在 `plugin.yaml` 同目录下。
- **绝对 `http(s)://` URL**：宿主直接透传给前端，插件作者可自行托管图标。
- **都不写**：前端回退到内置图标映射或首字母占位图。

安全约束：宿主只服务「已注册外部插件」声明的图标文件，且只允许图片扩展名（`.png` `.jpg` `.jpeg` `.svg` `.webp` `.gif` `.ico`），单个文件不超过 5 MB。插件无法通过该端点读取目录外的任意文件。

### 1.0.3 与内置连接器的关系

外部插件走的是 `ConnectorRegistry` 的 factory 通道（`RegisterFactory`），与内置连接器（飞书、语雀、Notion 等走 `Register` 单例通道）**并存、互不影响**：

- 内置连接器的实现、注册和调用路径**原样保留**，插件框架只是额外给它们登记了生命周期控制面（`BuiltinRuntime` 占位），不改变它们怎么干活；
- 宿主调用时通过 `GetForScope` 先查外部 factory、查不到再回退到内置单例，所以内置连接器在插件框架下照常工作；
- **注意**：外部插件通过 `metadata.connector_type` 自定义的类型名，**不能与内置类型名（如 `feishu`、`yuque`、`notion`）冲突**。因为「factory 优先」的查找顺序，同名的外部插件会覆盖内置实现。插件作者应使用自己独立的类型名前缀（例如 `yourorg.feishu-proxy`）避免冲突。

## 1.1 gRPC 协议与配置契约

插件运行时通信使用 `pkg/pluginapi/proto/plugin.proto` 定义的 Protobuf。协议方法名是强约束（`Validate` / `ListResources` / `FetchAll` / `FetchIncremental` / `Parse` / `Search`），但**请求体里的部分字段通过 `google.protobuf.Struct` 传递**——字段名与类型在运行期校验。使用 SDK 时无需关心这一层。

- 数据源使用 `DataSourceRequest` / `DataSourceResponse`；
- 解析器使用 `ParserRequest` / `ParserResponse`；
- 网络搜索使用 `WebSearchRequest` / `WebSearchResponse`；
- 握手与健康检查统一由 `PluginControl` service 承载（所有扩展类型通用），使用 `HandshakeRequest`、`HandshakeResponse`、`HealthRequest`、`HealthResponse`；业务 service（`DataSourcePlugin` / `ParserPlugin` / `WebSearchPlugin`）只负责各自的业务方法，不再包含 Handshake/Health；
- `DataSourceRequest.config` 在 v1 仍使用 `google.protobuf.Struct`，但宿主会依据 Manifest 的 `config_schema` 在请求发出前做必填项、类型和枚举校验，插件作者不需要猜字段；
- `DataSourceResponse.cursor` 使用 `Cursor.data` 的 `bytes`，由插件自行编码和解码，推荐 JSON 或 protobuf 私有消息。
- `FetchedItem.updated_at` 使用 RFC3339/RFC3339Nano 字符串传输；格式错误会被宿主拒绝，不会静默替换为当前时间。

外部插件只需要依赖 `pkg/pluginapi` SDK，不需要手写 gRPC service descriptor。SDK 会把普通 Go 结构转换为 Protobuf 消息。

### 1.1.1 配置字段的 `url` 类型与 SSRF 校验

配置字段声明为 `type: url` 时，宿主会在配置进入插件**之前**自动做 SSRF 安全校验（与内置连接器使用同一套 URL 防护）：

```yaml
config_schema:
  type: object
  properties:
    settings:
      type: object
      required: [base_url]
      properties:
        base_url:
          type: url    # 用 url 而非 string
```

宿主会拦截内网 IP、回环地址、云 metadata 地址（`169.254.169.254` 等）以及非 http/https 协议。**插件作者只需把由用户配置的 URL 字段声明为 `url` 类型，宿主会在配置进入插件前进行校验**；插件自身发起出站请求时仍应使用 SDK 提供的受保护 HTTP 客户端。

对于声明 `network: allowlist` 的插件，宿主会依据 `permissions.allowed_destinations`
（域名白名单，支持 `*.example.com` 通配）向插件进程注入环境变量，SDK 再从环境变量
读取策略，并在出站时按**域名**（而非字符串前缀）匹配放行。插件必须使用
`pluginapi.NewPluginHTTPClient()` 才会经过这层 SDK 防护；`ProcessRuntime` 本身不提供
操作系统级的网络隔离，不能把它当作安全边界。

部署管理员还可以为每个插件设置可信度：`offline`（默认断网）、`trusted`
（仅适用于可信的 ProcessRuntime 插件，遵循 Manifest 白名单）或 `isolated`
（必须使用 `docker://` OCI 入口；联网时由宿主为该插件创建独立的 Unix Socket
出口代理）。可信度配置通过系统设置 `plugins.trust_levels` 持久化，格式为
JSON 对象，例如 `{"weknora.dingtalk":"trusted","third.party":"isolated"}`；
也可以使用 `WEKNORA_PLUGIN_TRUST_LEVELS` 作为数据库未配置时的部署级回退。插件的
Manifest 权限始终是上限，可信度配置不能扩大其允许访问的目标。

**可信度变更不会立即生效**：插件在加载时确定其可信度，修改后需要下一次
「刷新插件」（rescan，重新装载）或宿主重启才会重新解析并应用。因此修改可信度后
需要触发 rescan；前端插件管理页会用「待生效」圆点提示尚未应用的变更。

模板仓库里的 `go.mod` 可能包含 `replace github.com/Tencent/WeKnora => ../..`，
这只是当前源码树内联调的便利配置。复制到真正独立仓库后，应删除该
`replace`，改为依赖发布版 WeKnora SDK，或把经过审核的 `pkg/pluginapi` 版本
vendor 到插件仓库；插件运行时仍然不需要链接宿主主程序。

### 1.2 推荐的配置声明

```yaml
config_schema:
  type: object
  properties:
    settings:
      type: object
      required: [root]
      additionalProperties: false
      properties:
        root:
          type: string
          description: Directory to scan
        include_hidden:
          type: boolean
          default: false
```

插件必须使用 `config_schema` 声明配置。

### 1.2.1 凭证分区（credentials）

带凭证的数据源必须把敏感凭证放在 `credentials` 分区，
而不是 `settings`。宿主对此有两个硬性行为：

- 前端把 `config_schema` 渲染成两个表单区：`properties.settings`（基本设置）和
  `properties.credentials`（凭证，以密码框显示）；
- 使用仅验证凭证的测试接口时，宿主会以 `credentials` 构造验证请求；正式同步和资源浏览会根据调用场景传递完整的 `settings` 与 `credentials`。

因此凭证字段（如 `app_secret`、`token`）必须声明在 `properties.credentials` 下，
否则插件无法从约定的凭证分区读取它们。完整示例：

```yaml
config_schema:
  type: object
  properties:
    settings:
      type: object
      required: [repositories]
      additionalProperties: false
      properties:
        repositories:
          type: array
          description: 逗号分隔的仓库，如 owner/repo 或 owner/repo:branch
    credentials:
      type: object
      additionalProperties: false
      required: [app_key, app_secret]
      properties:
        app_key:
          type: string
          description: 应用 AppKey（Client ID）
        app_secret:
          type: string
          secret: true      # 敏感字段，前端以密码框显示
          description: 应用 AppSecret（Client Secret）
```

插件端从 `request.Config["credentials"]` 读取凭证、从 `request.Config["settings"]`
读取普通配置。无凭证的数据源（如本地目录）可以只声明 `settings`，不写
`credentials`——前端会跳过凭证步骤。

凭证的权威副本在宿主：凭证以 AES-256-GCM 加密存储在数据源记录中，每次调用
（校验 / 资源浏览 / 同步）时随 `request.Config` 透传给插件。插件应保持无状态，
不缓存、不持久化凭证——因此进程重启或容器替换后凭证不会丢失，也无需实现任何
恢复逻辑。

### 1.3 调用上下文（InvocationContext）

宿主通过 gRPC metadata 传递调用关联信息，不修改 v1 protobuf，旧插件可以直接忽略：

```go
invocation := pluginapi.InvocationContextFromContext(ctx)
// invocation.DataSourceID
// invocation.OperationID
// invocation.TraceID
```

这些字段用于日志、审计和追踪，不是权限凭据。插件不得根据 metadata 中的租户或数据源 ID 绕过自身配置校验；宿主仍然是权限和数据归属的唯一权威。

## 1.4 生产运行治理

运行治理由宿主负责，插件不需要实现并发控制、排队、租约锁、重启或消息投递。宿主会为插件调用提供独立的并发额度和有限等待队列；队列满、排队超时或 Runtime 正在退出时返回可重试错误。Handshake 和 Health 属于控制面，不应被业务限流阻塞。

数据源同步的去重、分布式互斥、断电恢复和消息可靠投递同样由宿主负责。插件只需要正确响应 `context.Context` 的取消，并保证同一次请求在重复调用时不会破坏源端游标或返回不一致的数据。

## 2. 实现 SDK Handler

核心数据源函数是四个，外加一个可选的祖先解析回调：

```go
pluginapi.DataSourceHandler{
    PluginID: "your-org.example-datasource",
    OnValidate: func(ctx context.Context, req pluginapi.Request) error { ... },
    OnListResources: func(ctx context.Context, req pluginapi.Request) ([]pluginapi.Resource, error) { ... },
    OnFetchAll: func(ctx context.Context, req pluginapi.Request) ([]pluginapi.FetchedItem, error) { ... },
    OnFetchIncremental: func(ctx context.Context, req pluginapi.Request) ([]pluginapi.FetchedItem, map[string]any, error) { ... },
    // 可选：解析资源的祖先链（返回祖先 ExternalID 列表），宿主用它补全层级关系
    OnResolveResourceAncestors: func(ctx context.Context, req pluginapi.Request) ([]string, error) { ... },
}
```

> `OnResolveResourceAncestors` 是可选的；不实现时对应 RPC 返回空。它用于返回某个资源在源端的祖先 ExternalID 列表（例如目录树的父级路径），宿主据此补全父子关系。

大数据源可以使用可选的流式回调，按批次发送项目并在 cursor 到达安全边界时发送 checkpoint：

```go
OnFetchAllStream: func(ctx context.Context, req pluginapi.Request, emit func(pluginapi.Response) error) error {
    for _, item := range fetchNextPage(req) {
        if err := emit(pluginapi.Response{Items: []pluginapi.FetchedItem{item}}); err != nil {
            return err
        }
    }
    return emit(pluginapi.Response{Cursor: nextCursor})
},
```

宿主会在收到带 cursor 的响应后调用 `StreamHandler.Checkpoint`。如果插件只实现 unary 回调，SDK 仍会提供兼容的单批 fallback；这种模式适合中小规模数据，不具备同步中途恢复能力。

启动代码读取 `WEKNORA_PLUGIN_ADDR`，然后调用 `pluginapi.Serve(ctx, address, handler)`。主程序会先调用 `Handshake` 和 `Health`，再执行同步。

## 3. 数据契约

### 3.1 请求 `Request`（SDK 结构体字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| `Config` | `map[string]any` | 宿主透传的用户配置（含凭证，已按 `config_schema` 校验） |
| `ResourceIDs` | `[]string` | 本次要同步的资源 ID 列表 |
| `ParentID` | `string` | 父资源 ID（`ListResources` 懒加载子节点时用） |
| `Cursor` | `map[string]any` | 上次同步返回的游标（增量同步时） |

### 3.2 响应 `Response`（unary 返回值与流式 `emit` 共用）

| 字段 | 类型 | 说明 |
|---|---|---|
| `Error` | `string` | 致命错误信息，非空即整批失败（**不要用它上报单条失败**，见 3.6） |
| `Resources` | `[]Resource` | 资源列表（`ListResources` 返回） |
| `Ancestors` | `[]string` | 祖先 ExternalID 列表（`ResolveResourceAncestors` 返回） |
| `Items` | `[]FetchedItem` | 拉取的文档条目 |
| `Cursor` | `map[string]any` | 本次同步后的新游标 |
| `Warnings` | `[]string` | 预留的告警字段。宿主当前不消费，不要用它上报部分失败（见 3.6） |

### 3.3 `Resource` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `ExternalID` | `string` | 资源在源端的稳定 ID |
| `Name` | `string` | 资源名称 |
| `ParentID` | `string` | 父资源 ID（树形结构） |
| `Type` | `string` | 资源类型（如 `wiki_space`、`document`） |
| `Metadata` | `map[string]string` | 附加信息 |

### 3.4 `FetchedItem` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `ExternalID` | `string` | 源端稳定 ID，**必填** |
| `Title` | `string` | 文档标题 |
| `FileName` | `string` | 文件名（含扩展名） |
| `Content` | `[]byte` | 文档内容（二进制） |
| `URL` | `string` | 原始 URL（仅 URL 无内容时用） |
| `MIMEType` | `string` | MIME 类型 |
| `UpdatedAt` | `string` | 最后更新时间（RFC3339） |
| `Metadata` | `map[string]string` | 元数据（来源、作者等）；单条抓取失败时用 `error` 键上报占位条目（见 3.6） |
| `IsDeleted` | `bool` | 标记删除（增量同步） |
| `ReplacesSubtree` | `bool` | 用本次返回项整体替换某子树 |
| `SubtreeKeep` | `[]string` | 子树替换时要保留的子项 ExternalID |

### 3.5 核心契约

`FetchedItem.ExternalID` 必须是源端稳定 ID，例如 `file:docs/readme.md` 或 `source:document:123`。不要把 WeKnora 的内部 DataSource ID 拼进去，否则导入/导出或重建数据源后会产生重复文档。

首次同步返回全部文件，并返回一个插件自定义的 cursor map。之后 `FetchIncremental` 收到上次返回的 cursor，只返回新增、修改和删除的项目。删除使用 `IsDeleted: true`；不要把删除的文件重新读成空文档。

目录型数据源可以用 `ReplacesSubtree: true` 声明「用本次返回的项整体替换某个子树」，并用 `SubtreeKeep` 列出要保留（不删除）的子项 ExternalID。这适用于「目录被整体重建/改名」这类场景，宿主据此做子树级替换，而不是逐项 diff。

文件型插件可以使用内容 SHA-256；远端 API 插件可以使用版本号、更新时间和分页 token。cursor 存在 WeKnora 数据库的数据源记录中，不属于插件进程，因此进程重启、容器替换后仍然可以继续增量同步。

### 3.6 增量拉取的返回形状

`FetchIncremental` / `OnFetchIncremental` 一次调用返回 `items + cursor + 错误`。宿主按下表处理，插件作者务必对齐：

| 返回形状 | 插件侧表达 | 宿主行为 | cursor |
|---|---|---|---|
| 成功 | 返回 `Items` + `Cursor`，`Error` 为空 | 处理全部 items，同步记 `success` | 推进 |
| 单条失败（可继续） | 在 `Items` 中放**占位条目**：`Metadata["error"]` 非空，且 `Content`、`URL` **均为空** | 该条计入失败数并写入同步日志的错误样本，**其余条目照常入库**，同步记 `partial` | **不推进**，下轮自动重试 |
| 全部失败 | 每个需同步的条目都按上一行方式返回占位条目 | 同步直接判 `failed` | **不推进** |
| 致命失败 | `Response.Error` 非空 | **丢弃本次 items、不推进 cursor**，任务判失败并按重试策略重跑 | 不推进 |

要点：

1. **部分失败用「占位条目」，不要把整体作为致命失败返回。** 这是宿主真正消费、内置连接器（语雀 / 飞书）普遍使用的通道：某篇文档抓取失败时，只为它生成一个带 `Metadata["error"]` 的空内容占位条目、继续同步其余文档，而不是中断整批。只有「一个都取不到」这类无法降级的情形才用 `Error` 致命失败。

   SDK 已经提供构造器，直接调用即可——它保证 `Content` 与 `URL` 为空，避免手写时漏掉触发条件：

   ```go
   // 单条失败：计入失败数，其余条目照常同步，下轮自动重试该条
   items = append(items, pluginapi.FailedItem(docID, doc.Title, err.Error()))

   // 需要前端按语言本地化时，附带稳定的 i18n code 与参数
   items = append(items, pluginapi.FailedItemWithReason(
       docID, doc.Title, err.Error(), "feishu_api_error", "1663"))
   ```

   对应的 metadata 键由 SDK 导出为常量（`pluginapi.MetadataKeyError`、`MetadataKeyErrorReasonCode`、`MetadataKeyErrorReasonCodeValue`、`MetadataKeyErrorReason`），不需要手写字符串。
2. **占位条目的触发条件很严格**：必须 `Content` 与 `URL` **都为空**，且 `Metadata["error"]` 存在，宿主才会把它计为失败。若把错误信息塞进 `Content`，该条会被当成正常文档入库；若只留一个空条目而不带 `Metadata["error"]`，则会被静默计为 `Skipped`（两类都造成漏报）。
3. **占位条目必须携带稳定的 `ExternalID`**：失败会让宿主保留上一轮 cursor，下一轮重新拉取同一批变更（含本批已成功的条目，见 3.7 的 at-least-once），日志与重试都依赖该 ID 定位。
4. **错误样本可结构化**：`Metadata["error"]` 作为兜底原文；若同时提供 `error_reason_code`（稳定的 i18n code）、`error_reason_code_value`、`error_reason`，宿主会以 code + 参数形式记录，前端可按语言本地化展示（内置飞书连接器即如此），原始状态码与响应体只留在服务端日志。
5. **流式路径同理**：`emit` 出的占位条目同样计入失败计数；若整批条目全部失败，宿主会丢弃已 checkpoint 的 cursor，下一轮从头重试。
6. **致命失败时不要指望 cursor 被保存**：此时宿主不处理 items、也不保存 cursor；即使插件“顺便”返回了 cursor 和部分 items，也会被一并丢弃。这是为了避免把未处理的文档推进到 cursor 之后、造成永久漏同步。
7. **`Response.Warnings` 不参与判定**：该字段当前不被宿主消费，不能用它上报部分失败。进程内连接器另有 `PartialFetchError` 通道（宿主将其降级为告警、继续处理，并以 `partial` 呈现），但该通道**不在 gRPC 协议内，进程外插件无法使用**——插件表达部分失败的唯一方式是上一表的「单条失败（可继续）」。

### 3.7 一致性边界（插件作者须知）

增量同步采用 **at-least-once** 语义：当某个条目处理失败时，宿主**不推进 cursor**，下一轮会重新拉取同一批变更（包括本批已经成功的条目）。因此：

1. **条目处理必须幂等**：同一个 `ExternalID` 被重复返回时，必须不产生重复数据。内容未变的重放会被宿主去重跳过；内容变更的更新会替换为新版本。请保证你的 `OnFetchIncremental` 与后端写入在重复调用下结果一致。
2. **失败会重放，直到成功**：这是 at-least-once 的代价——某个文档若始终处理失败，cursor 会被它卡住，后续每轮重复拉取同一批。该失败会以“部分成功”状态（含失败计数）记录在同步日志中，不会静默丢失。
3. **删除要显式表达**：源端删除必须用 `IsDeleted: true` 返回，不要返回空文档或直接省略。

## 4. 构建、安装和验证

```powershell
go mod tidy
go build -o weknora-plugin.exe .
```

把插件目录放进 WeKnora 的插件目录，目录中包含 `plugin.yaml` 和可执行文件。主程序发现 Manifest 后校验版本、权限、协议版本，再按 Manifest 的 `entrypoint` 以进程方式或容器方式启动。

数据源插件目录示例：

```powershell
$env:WEKNORA_PLUGIN_DIR_DATASOURCE = "D:\weknora-plugins\datasource"
```

其他扩展点使用各自的插件目录变量，详见对应扩展点文档。未设置的数据源插件目录会被忽略；如果显式设置了路径，但路径不存在或不是目录，主程序会返回错误（例如 `stat plugin root ... not a directory`），而不是静默跳过。一个目录可以放置多个独立插件包：

```text
D:\weknora-plugins\datasource\
├── plugin-a\
│   ├── plugin.yaml
│   └── weknora-plugin.exe
└── plugin-b\
    ├── plugin.yaml
    └── weknora-plugin.exe
```

每个目录内可以放多个独立插件包，扫描时递归查找 `plugin.yaml` / `plugin.yml` / `plugin.json`。插件的扩展类型以 Manifest 里的 `extension_type` 字段为准；同一个 `id` 重复时主程序会拒绝启动，避免加载顺序造成歧义。

### 4.1 运行环境与构建目标（ProcessRuntime）

外部插件通过 `entrypoint` 由宿主启动。`entrypoint` 指向的可执行文件**必须与宿主的运行环境匹配**，否则插件无法启动，宿主会报告装载或连接错误（例如 `connect plugin process: context deadline exceeded`、`stat plugin root ... not a directory`）：

| 宿主运行方式 | 宿主的操作系统/架构 | 插件应使用 |
| --- | --- | --- |
| 本地直接 `go run` / 跑编译后的 server | 与当前开发机一致 | 与开发机一致的原生二进制（如 Windows 下 `.exe`） |
| docker 容器（`app` 服务跑在 Linux） | **Linux/amd64** | **Linux 二进制**（无 `.exe`），由 `GOOS=linux GOARCH=amd64 go build` 产出 |
| WSL 内启动宿主 | Linux（WSL 发行版） | Linux 二进制 |

要点：

- `WEKNORA_PLUGIN_DIR_DATASOURCE` 等目录变量填的必须是**宿主进程能访问到的路径**。docker/WSL 下用容器/挂载路径（如 `/mnt/d/weknora-plugins/datasource`），不要填 Windows 盘符（`D:\...` 在 Linux 里会被解析成相对路径 `D`，导致 `stat plugin root "D"` 报错）。
- 使用 ProcessRuntime 时，插件作为宿主进程的**子进程**运行，共享宿主的网络命名空间。因此它不具备操作系统级的网络隔离能力；需要联网的插件应确认运行环境允许其声明的网络策略。`DockerRuntime` 始终使用 `--network none`；若 OCI 插件声明受控联网策略，宿主会为该插件启动独立的 Unix Socket 出口代理，插件只能通过代理访问经策略允许的公网目标。
- `go.mod` 里的 `replace github.com/Tencent/WeKnora => ../..` 这类路径在 Windows 和 WSL 间不通用；若需跨环境构建，建议改用相对路径（如 `../../../WeKnora-fork`）或发布版 SDK 依赖，避免构建环境差异导致编译失败。

第三方作者可以使用 SDK 的 `pluginapi.RunDataSourceConformance` 对已经连接的 gRPC client 做握手、健康、校验、全量和增量接口的冒烟测试。该函数签名要求分别传入控制面 client（`PluginControlClient`，负责握手与健康检查）和业务 client（`DataSourcePluginClient`，负责校验/同步），两个 client 共享同一个 gRPC 连接。集成测试还应检查：首次同步能够导入预期项目，源端只变更一个项目时插件只返回该项目，删除项目时按约定返回删除标记；单条抓取失败时返回带 `Metadata["error"]` 的占位条目、且不中断整批（见 3.6）。

流式插件还应验证：同步过程中收到多个响应批次；每个 cursor 都能被重新传给插件；在 checkpoint 后模拟进程中断，重启后不会重复处理已经确认的项目，也不会漏掉未确认项目。

进程级集成测试建议直接指向插件目录、读取其真实 Manifest，并覆盖进程启动、gRPC 调用、连接器解析与 cursor 传递路径。具体的测试变量与命令以你的插件仓库 README 为准。

## 5. 独立开发自检

一个完整的数据源插件，应当能在**不修改 WeKnora 主仓**的前提下独立完成：

1. Manifest 的 ID、名称与配置字段由插件自定义；
2. 实现所需的 Handler（至少包括 `OnValidate`、`OnFetchAll` 和 `OnFetchIncremental`）；
3. 独立构建并启动 gRPC 服务；
4. 被主程序发现、握手、通过健康检查；
5. 完成至少一个文件的全量同步与增量同步。

这些环节只依赖本指南与 `pkg/pluginapi` SDK。如果你的实现必须修改 WeKnora 主仓（例如宿主内部的连接器注册表或同步流程）才能跑通，说明插件契约尚未覆盖该场景——这属于宿主/SDK 需要补的能力，请反馈，而不是在你的插件里绕开。

## 5.1 运行期健康巡检

宿主负责定期调用插件的 `Health`，并根据结果管理运行状态、draining、重启和失败恢复。插件只需要通过 SDK 正确提供控制面健康响应，并在收到 `context.Context` 取消时尽快结束业务调用。自动重启、最大重试次数和巡检间隔由部署环境配置，不属于插件业务代码。

## 6. 其他扩展点的制作指南

五类扩展点各自有独立的插件制作文档。本文件只讲数据源；其余四类的指南见：

| 扩展点 | `extension_type` | 文档 |
| --- | --- | --- |
| 文档解析 | `parser` | [plugin-development-parser.md](plugin-development-parser.md) |
| 网络搜索 | `search` | [plugin-development-websearch.md](plugin-development-websearch.md) |
| 模型管理 | `model` | [plugin-development-model.md](plugin-development-model.md) |
| 检索引擎 | `retriever` | [plugin-development-retriever.md](plugin-development-retriever.md) |

它们共享本文第 1、1.1、1.2、1.3、1.4、4 节描述的同一套 manifest / runtime / manager / 权限 / 治理骨架，只是各自的业务协议与 SDK Handler 不同。开发对应插件前，请先读本文，再读对应扩展点的文档。

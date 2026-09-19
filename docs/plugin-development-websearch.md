# 网络搜索（Web Search）插件制作指南

> 本文面向要独立开发一个 **网络搜索（Web Search）扩展点插件** 的开发者。
> 它只依赖本仓库的 `pkg/pluginapi` SDK 和主仓的插件扫描机制，无需改动 WeKnora 主仓源码。
>
> 五类扩展点共用的插件骨架见本文第 2 节。本文只讲 Web Search 特有的部分。

## 1. Web Search 插件是做什么的

Web Search 插件负责在「联网搜索」环节，把一个查询交给某个搜索引擎（或自有搜索服务），返回一批结果（标题、URL、摘要、正文、来源、发布时间）。它对应主仓的网络搜索 Provider 注册机制。

宿主在用户触发联网检索时，按 provider 类型找到对应插件，把查询透传过去，拿到结果后交给后续的融合/引用流程。

## 2. 五类扩展点的统一骨架（速览）

Web Search 插件与其他四类一样，遵循同一套发现与生命周期协议：

| 项 | Web Search 插件取值 |
|---|---|
| `api_version` | `weknora.plugin/v1`（必填） |
| `id` | 必填，插件唯一 ID |
| `name` | 必填，插件显示名称 |
| `version` | 必填，插件版本 |
| `extension_type` | `search`（必填） |
| `protocol_version` | `v1`（必填） |
| `weknora_version` | 可选，宿主兼容范围 |
| `entrypoint` | 必填 |
| 环境变量 | `WEKNORA_PLUGIN_ADDR` |

> 完整字段说明（含 `capabilities`、`config_schema`、`permissions`、`metadata`）见 [数据源插件指南第 1 节](plugin-development-datasource.md#1-插件包结构)。

启动后必须实现两个统一服务：

- `PluginControl`：`Handshake` + `Health`。
- `WebSearch`：本扩展点特有的检索协议（见下）。

用 SDK 的 `ServeWebSearch` 一行即可同时挂起这两个服务。

## 3. 核心协议：`WebSearchPlugin` 服务

```text
rpc Search(WebSearchRequest) returns (WebSearchResponse)
```

### 3.1 请求 `WebSearchRequest`（SDK 结构体字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| `Query` | `string` | 搜索查询词 |
| `MaxResults` | `int` | 期望返回的最大结果数 |
| `IncludeDate` | `bool` | 是否要求返回发布时间 |
| `APIKey` | `string` | 搜索引擎 API Key（由宿主从配置注入） |
| `EngineID` | `string` | 自定义搜索引擎 ID |
| `BaseURL` | `string` | 搜索服务地址 |
| `ProxyURL` | `string` | 代理地址 |
| `ExtraConfig` | `map[string]string` | 其他引擎特定配置 |

### 3.2 响应 `WebSearchResponse`（SDK 结构体字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| `Results` | `[]*WebSearchResult` | 结果列表 |
| `Error` | `string` | 错误信息，非空表示失败 |

`WebSearchResult` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Title` | `string` | 标题 |
| `URL` | `string` | 链接 |
| `Snippet` | `string` | 摘要 |
| `Content` | `string` | 正文 |
| `Source` | `string` | 来源（通常是搜索引擎名） |
| `PublishedAt` | `string` | 发布时间 |

关键语义约束：

1. **错误要显式**：`OnSearch` 返回 error 时，SDK 会自动把 error 填入 `Error` 字段（若你未手动填）；宿主看到 `Error` 非空就按失败处理。
2. **`Source` 要诚实**：结果来源要真实标注，供宿主引用展示，不能伪造。
3. **遵守 `MaxResults`**：尽量不超过请求上限，避免宿主侧结果过多。

## 4. SDK 侧如何实现：`ServeWebSearch`

```go
package main

import (
    "context"
    "os"

    "github.com/Tencent/WeKnora/pkg/pluginapi"
)

type mySearch struct{}

func (mySearch) Search(ctx context.Context, req pluginapi.WebSearchRequest) (pluginapi.WebSearchResponse, error) {
    results, err := callMySearchEngine(req.Query, req.MaxResults, req.APIKey)
    if err != nil {
        return pluginapi.WebSearchResponse{}, err // SDK 会填到 Error 字段
    }
    return pluginapi.WebSearchResponse{Results: results}, nil
}

func main() {
    handler := pluginapi.WebSearchHandler{
        PluginID:     "example.mysearch",
        Capabilities: []string{"search"},   // 必须与 plugin.yaml 的 capabilities 一致
        OnSearch:     (mySearch{}).Search,
    }
    addr := os.Getenv("WEKNORA_PLUGIN_ADDR")
    if err := pluginapi.ServeWebSearch(context.Background(), addr, handler); err != nil {
        panic(err)
    }
}
```

要点：

- `WebSearchHandler.PluginID` 必须与 `plugin.yaml` 的 `id` 一致。
- `WebSearchHandler.Capabilities` 必须与 `plugin.yaml` 的 `capabilities` **逐项一致**——宿主握手时会做双向严格校验：manifest 声明了但 runtime 未回显、或 runtime 回显了 manifest 未声明，都会导致装载失败。本文示例统一用 `"search"`。
- `OnSearch` 为 nil 时返回 `Error: "web search handler is not implemented"`。
- `Handshake` / `Health` 由 `WebSearchHandler` 自动实现。
- 出站请求请用 `pluginapi.NewPluginHTTPClient()`（读取宿主注入的网络策略），不要用裸 `http.Client` / `http.DefaultClient`——否则 `permissions.network` 白名单不生效，等于绕过了框架的安全模型。

## 5. `plugin.yaml` 示例

```yaml
api_version: weknora.plugin/v1
id: example.mysearch
name: My Search Provider
version: 1.0.0
extension_type: search
protocol_version: v1
weknora_version: ">=0.7 <1.0"
entrypoint: ./mysearch

capabilities:
  - search              # 与 handler 的 Capabilities 一致（值是任意字符串，示例统一用 search）

metadata:
  provider_type: mysearch           # 供宿主注册与路由的 provider 类型名
  description: My Search Provider   # 可选：provider 描述，前端展示
  icon: logo.png                    # 可选：插件图标（相对插件目录的文件名，或 http(s) URL）
  docs_url: https://example.com     # 可选：官方文档链接，前端渲染"查看文档"入口

config_schema:
  type: object
  properties:
    settings:                       # 非敏感配置：保留 key 与自定义参数都放这里
      type: object
      required: [base_url]
      properties:
        base_url:                   # 保留 key：宿主渲染专用「Base URL」框
          type: string
          title: Search API URL
        engine_id:                  # 保留 key：宿主渲染专用「Engine ID」框
          type: string
          title: Engine ID
        proxy_url:                  # 保留 key：宿主渲染专用「HTTP 代理」框
          type: string
          title: HTTP Proxy
        search_depth:               # 自定义参数：宿主渲染成下拉框，值经 ExtraConfig 透传
          type: string
          title: Search Depth
          enum: [basic, advanced]
          default: basic
    credentials:                    # 敏感凭证：search 的 credentials 只允许 api_key
      type: object
      required: [api_key]
      properties:
        api_key:
          type: string
          title: API Key
          secret: true              # 敏感字段；必须用 secret: true，而不是 type: secret

permissions:
  network: allowlist                # 联网插件必须声明网络策略
  allowed_destinations:             # 白名单：写域名（可带 :端口），支持通配，不带 https:// 前缀
    - "api.example.com"
    - "*.example.com"
```

### 保留 key 的硬约定（影响前端表单）

宿主通过 `config_schema` 的**分区 + 字段名**来识别 provider 管理界面的元数据，以下四个字段名是**硬约定**，必须恰好同名，且放在对应分区：

| 字段名 | 所在分区 | 宿主识别结果 |
|---|---|---|
| `api_key` | `credentials` | `RequiresAPIKey` —— 前端渲染"需要 API Key"标识 |
| `engine_id` | `settings` | `RequiresEngineID` —— 前端渲染"需要 Engine ID"标识 |
| `base_url` | `settings` | `RequiresBaseURL` —— 前端渲染"需要 Base URL"标识 |
| `proxy_url` | `settings` | `SupportsProxy` —— 前端渲染"支持代理"标识 |

不要改名（如 `apikey`、`key`、`token`）——改名后宿主不会把这些字段识别为对应能力，前端表单与参数校验都会失效。

这四个保留 key 的行为语义：

- 它们**只驱动能力标志与宿主内置的专用表单区**（API 密钥框、Base URL 框、HTTP 代理框），**不会**作为自定义字段再渲染一遍输入框。
- `required` 决定对应输入框**是否出现**：`required: true` 显示（且必填），`required: false` **完全不显示**——框架没有"可选显示"的中间态。需要一个"可留空"的 Base URL 时，请声明 `required: true` 并让用户填默认地址。
- 其他自定义字段声明在 `settings` 分区，宿主会把它们转成可输入的控件（见下节），不会重复渲染保留 key。

### 扩展参数（config_schema）

插件的**自定义参数**（搜索深度、返回条数、语言等）声明在 `config_schema.properties.settings` 下——宿主会把它转换成前端可渲染的字段列表，参数值经请求的 `ExtraConfig` 透传给插件。保留 key（`api_key` / `engine_id` / `base_url` / `proxy_url`）不会作为自定义字段重复出现。

写法（放在 `settings` 分区下，key 即参数名）：

```yaml
config_schema:
  type: object
  properties:
    settings:
      type: object
      properties:
        search_depth:
          type: string
          title: Search Depth             # 表单 label
          description: basic 或 advanced  # 输入提示与说明文字
          enum: [basic, advanced]         # 有 enum 时渲染为下拉框
          default: basic
        page_size:
          type: integer                   # 渲染为数字输入框
          title: Page Size
          default: 5
        safe_mode:
          type: boolean                   # 渲染为开关
          title: Safe Mode
```

字段属性与控件映射：

| 声明 | 前端控件 |
|---|---|
| 有 `enum` | 下拉框（options 取自 enum） |
| `type: string` | 文本输入框（`secret: true` 时为密码框） |
| `type: integer` / `number` | 数字输入框 |
| `type: boolean` | 开关 |
| `type: array` / `string[]` | 逗号分隔文本输入 |

约定与边界：

- **值统一为字符串**：无论声明什么类型，参数值都以字符串存入 `ExtraConfig`（`"5"`、`"true"`），插件侧自行解析。
- **扩展字段明文存储**：`extra_config` 不做加密/脱敏。**敏感凭证一律用 `credentials` 分区的 `api_key`**（走加密），不要在 `settings` 里声明 secret 类凭证字段（`secret: true` 只能出现在 `credentials` 分区，且 search 的 `credentials` 只允许 `api_key`）。

### 网络策略（联网插件必读）

Web Search 插件需要出站联网，所以 `permissions.network` 必须是 `allowlist`：

- `allowlist`：白名单模式，**必须同时提供 `allowed_destinations`**（宿主装载时校验：`allowlist` 且无白名单会直接装载失败）。

白名单匹配按**域名（host）+ 通配**，不是字符串前缀匹配——写 `api.example.com` 不会误放行 `api.example.com.evil.com`。合法写法是「精确域名」（`api.example.com`）、「域名:端口」（`api.example.com:443`）或「`*.example.com`」（匹配该域下的子域）；**不支持单独写 `*` 来放行全部**。

**反 SSRF 边界（重要）**：出站守卫会拦截内网/私有地址（`IsForbiddenIP` 含 `IsPrivate()`）——即使 `allowed_destinations` 显式写入了内网 host，连接仍会被拒绝。因此**对接内网搜索服务的插件无法出站**，请改用公网搜索 API。此外，请求里的 `BaseURL` 覆盖目标同样受 allowlist 约束：指向白名单外的域名会被拦截。

## 6. 校验与验证

SDK 提供 conformance 测试入口 `RunWebSearchConformance`，可对插件做协议级自检：

- 握手回报 `extension_type=search`；
- 正常查询返回结构化结果；
- 错误查询显式报错。

建议：本地起 server → 跑 conformance → 通过后再放入 `WEKNORA_PLUGIN_DIR_SEARCH` 指向的目录交给宿主装载。

没有真实 API Key 时，conformance 的 Search 用例无法通过（会真实出网）。此时的最小验证路径：起进程 → Handshake 回报 `extension_type=search` → Health 返回非空 state——这两项不依赖任何凭证；Search 的端到端留给拿到 Key 后的真机联调（宿主「设置 → 插件服务 → 刷新插件」装载后，在搜索设置里点「测试连接」）。

## 7. 宿主侧如何被使用

Web Search 与数据源、Parser 的区别是：它不是一次性同步，而是一个按租户配置即时调用的 `ProviderFactory`。外部插件进程可长期运行，宿主每次从租户配置创建一个轻量代理，把该租户的参数随请求传入插件。

加载后，宿主会把 Web Search 插件注册为一个普通的搜索 provider，既有的 provider 解析、租户参数传递、结果过滤与后续 RAG 压缩流程无需按插件 ID 增加分支。API Key 不会写入插件进程全局状态，而是由宿主创建 provider 实例时按租户传给每次 Search 请求。

## 8. 与宿主的关系（边界）

Web Search 插件**只负责「查询 → 搜索结果列表」**，不负责：

- 结果与知识库检索的融合（RRF 等）——宿主处理；
- 结果内容的抓取/解析——那是 Parser / DataSource 的职责；
- 权限过滤、租户隔离——宿主处理。

插件内部可自行决定调用哪个搜索引擎、如何翻页、如何限流，宿主不关心。

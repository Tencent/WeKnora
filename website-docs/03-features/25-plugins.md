# 插件

插件是 WeKnora 扩展能力的打包与分发单元：模型厂商、数据源、联网搜索、文档解析、技能、MCP 服务等都可以由插件提供。内置能力本身也以内置插件的形式登记，和第三方插件共用一套目录与开关。

两类角色分工如下：

| | 系统管理员 | 空间管理员 |
| --- | --- | --- |
| 做什么 | 安装、升级、回滚、卸载插件，填写平台配置 | 在本空间启用或停用插件，填写空间配置 |
| 入口 | 「设置 → 插件管理」 | 「设置 → 插件」 |

安装后的插件对所有空间可见，但**默认停用**，由各空间管理员自行启用。停用插件后，它的集成不再出现在类型列表中、不能新建，已有实例照常工作，并在列表中标注「插件已停用」。

## 插件的运行方式

插件包是一个 `.wkp` 文件（根目录带 `plugin.yaml` 的 zip）。`runtime.type` 决定插件代码在哪里运行：

| 运行方式 | 说明 | 适用 |
| --- | --- | --- |
| `declarative` | 没有代码，由 WeKnora 解释清单 | 技能、模型厂商定义、远程 MCP |
| `host` | WeKnora 的插件宿主拉起的子进程，`kind` 为 `binary` 或 `python` | 自托管的代码插件 |
| `remote` | 插件作者自行部署的 HTTP 服务，安装时登记地址 | SaaS 类插件、独立团队维护的服务 |

代码插件与 WeKnora 之间走统一的扩展协议 v1（HTTP + JSON，流式同步用 NDJSON）。

## 安装插件

1. 以系统管理员身份打开「设置 → 插件管理」，点击「安装插件」。
2. 上传 `.wkp` 或填写下载地址。WeKnora 先解析插件包，展示它提供的能力、申请的权限（可访问的外部域名、Host API 权限等）和包摘要。
3. 确认后安装。安装请求带着审阅时的摘要，下载地址在此期间换了内容会被拒绝。

同一插件再次安装更高版本即升级，旧版本保留，可在详情中回滚。

插件默认对所有空间可见（仍需各空间自行启用）。在插件详情的「可见范围」中可以改为只对指定空间可见：范围外的空间看不到该插件，也不能使用它的集成；它们原有的开关和配置保留，重新纳入后恢复。

### 插件市场

设置 `WEKNORA_PLUGIN_INDEX_URL` 指向一个插件索引后，「安装插件」中多出「从插件市场」，可按名称、ID 或发布者搜索，显示已安装版本与可升级版本。选中后照常审阅，安装时校验索引中登记的摘要，下载到的包与索引不符会被拒绝。

索引是一个 JSON 文件，任何能提供静态文件的地方都可以托管：

```json
{
  "schemaVersion": 1,
  "plugins": [
    {
      "id": "acme.search",
      "name": { "default": "Acme Search", "zh-CN": "Acme 搜索" },
      "description": { "default": "Search the Acme knowledge graph." },
      "publisher": { "id": "acme", "name": "Acme" },
      "icon": "https://plugins.example.com/acme-search.png",
      "categories": ["search"],
      "versions": [
        {
          "version": "1.1.0",
          "url": "packages/acme-search-1.1.0.wkp",
          "digest": "sha256:…",
          "engines": { "weknora": ">=0.5.0" }
        }
      ]
    }
  ]
}
```

- `url` 可以是相对索引地址的路径；`digest` 是插件包的 SHA-256（签名之后计算）。
- 列表中给出本平台能运行的最新版本；所有版本都不兼容的插件标为不兼容。
- 索引本身不决定可信度。审核插件的市场应该用自己的密钥签名插件包，平台把该密钥以 `verified` 级加入信任列表。

### 插件签名与信任等级

插件包可以带签名（包根目录的 `plugin.sig`）。WeKnora 按签名把插件包分为三级，安装审阅页和版本列表中会标出：

| 等级 | 含义 |
|---|---|
| 官方（official） | 由平台信任列表中 `official` 级的密钥签名，通常是 WeKnora 团队 |
| 已验证（verified） | 由平台信任列表中 `verified` 级的密钥签名，例如审核过插件的市场或平台认可的发布者 |
| 社区（community） | 未签名，或签名密钥不在信任列表中 |

签名覆盖包内除 `plugin.sig` 外的所有文件。签名存在但对不上（文件被改过、签名伪造，或密钥签了它无权签的发布者），安装直接被拒绝。

信任列表是一个 YAML 文件，路径由 `WEKNORA_PLUGIN_TRUSTED_KEYS` 指定：

```yaml
keys:
  - id: weknora-2026            # 签名时使用的密钥 ID
    publicKey: ed25519:BASE64...  # weknora-plugin keygen 输出的公钥
    level: official
  - id: acme-2026
    publicKey: ed25519:BASE64...
    level: verified
    publishers: [acme]          # 可选：只认该发布者的插件
```

`WEKNORA_PLUGIN_MIN_TRUST` 设置平台接受的最低等级，默认 `community`（全部接受）。调高后，低于该等级的插件包不能安装，也不能切换到这类版本；已安装的不再加载，详情中显示原因。

发布者用 SDK 自带的命令行生成密钥并签名：

```bash
go install github.com/Tencent/WeKnora/pluginsdk/cmd/weknora-plugin@latest
weknora-plugin keygen -out acme.key          # 输出公钥，交给平台管理员
weknora-plugin sign -key acme.key -key-id acme-2026 acme-search-1.0.0.wkp
weknora-plugin verify -pubkey ed25519:... acme-search-1.0.0.wkp
```

签名会改变插件包的摘要，请在签名之后再计算或公布摘要。

### 远程插件

`runtime.type: remote` 的插件在安装时还需填写服务地址：

- 服务在内网时，需把地址加入 `SSRF_WHITELIST`。
- 安装后 WeKnora 生成一个签名密钥，**只显示这一次**。把它配置为服务的 `WEKNORA_PLUGIN_SECRET` 环境变量，服务据此确认请求来自 WeKnora。
- 插件详情中可以修改服务地址或轮换密钥。轮换后，服务换上新密钥之前的调用都会失败。
- 服务版本必须与安装的插件包一致，否则插件显示为异常、调用被拒绝。升级时同时升级服务与插件包。

### 空间自有插件

系统管理员在「系统设置」中打开「允许空间登记自有插件」（`tenant.plugin_remote_enabled`，也可用环境变量 `WEKNORA_PLUGIN_TENANT_REMOTE`，默认关闭）后，空间管理员可以在「设置 → 插件」中登记本空间自有的插件：

- 只接受 `remote` 插件：代码运行在空间自己的服务器上，WeKnora 按服务地址调用，受 SSRF 白名单约束。
- 不能带页面（页面、编辑页、工具结果页），避免在 WeKnora 里嵌入空间自制的界面冒充平台。
- 只有登记它的空间可见，登记后自动在本空间启用；签名密钥只显示一次，之后可在「管理」中修改服务地址、轮换密钥、更新插件包或删除。
- 插件 ID 与平台插件、其他空间的插件不能重复。平台的最低信任等级同样适用。

关闭开关后不能再登记或升级，已登记的照常运行；系统管理员可在「插件管理」中看到并卸载各空间的自有插件。

## 运行代码插件

### 内嵌宿主（默认）

默认情况下，每个 app 进程自带插件宿主，在本机运行已安装的 `host` 插件：

- 插件进程只拿到 `WEKNORA_PLUGIN_*` 环境变量，看不到数据库密码等 WeKnora 密钥。
- 出网流量经宿主的出口代理，只放行插件在 `permissions.egress` 中声明的域名，内网地址一律拒绝。
- 宿主检查运行的进程与安装的包一致，健康检查失败时按退避重启，并在插件详情中显示为「异常」。
- Python 插件使用本机的 `python3`（可用 `WEKNORA_PLUGIN_PYTHON` 指定解释器）。Docker app 镜像已带 Python。
- 在 Linux 上，插件进程按 `runtime.resources` 限制资源：
  - 内存总是受限。插件没有声明时，按 `WEKNORA_PLUGIN_MEMORY_DEFAULT`（如 `1Gi`）限制，未设置则不限。
  - CPU 需要把一个可写的 cgroup v2 目录委托给 WeKnora，并通过 `WEKNORA_PLUGIN_CGROUP` 指定。设置后，每个插件进程进入各自的子 cgroup。
- 在 Linux 上设置 `WEKNORA_PLUGIN_NETNS=1`，每个插件进程运行在独立的网络命名空间中，只能经出口代理和 Host API 访问外部，无法绕过代理直连。
  - 需要系统允许非特权用户命名空间：Ubuntu 24.04 需将 `kernel.apparmor_restrict_unprivileged_userns` 设为 0，Docker 默认的 seccomp 配置会拦截。
  - 条件不满足时插件启动失败并在详情中说明原因，不会在不受限的情况下运行。
  - 独立 plugin-host 同样适用：插件经出口代理访问 app 节点的 Host API。

### 独立插件宿主

插件较多、希望把插件与 app 隔开，或 app 节点不便运行 Python 时，可以单独运行 `WeKnora plugin-host`：

- 它与 app 使用同一镜像，共用数据库、Redis、对象存储和 `SYSTEM_AES_KEY`（或 `JWT_SECRET`）。
- 启动后每 5 秒在 Redis 中通告自己运行的插件。app 按版本挑选最空闲的宿主，用由 `SYSTEM_AES_KEY` 派生的密钥签名调用它。
- 宿主停止时先撤回通告，再等进行中的调用结束。
- 插件详情的「节点状态」中，`plugin-host:` 开头的就是独立宿主。

docker compose 启用方式：在 `.env` 中设置

```bash
WEKNORA_PLUGIN_EMBEDDED_KINDS=none
WEKNORA_PLUGIN_HOST_API_URL=http://app:8080
```

然后执行：

```bash
docker compose --profile plugin-host up -d
```

Helm 设置 `pluginHost.enabled=true` 即可。使用本地存储（`STORAGE_TYPE=local`）时，插件包存放在 data-files 卷中，该卷需支持多 Pod 读写（ReadWriteMany）。

相关环境变量：

| 变量 | 作用于 | 说明 |
| --- | --- | --- |
| `WEKNORA_PLUGIN_EMBEDDED_KINDS` | app | app 自己运行的 kind（`binary`、`python`，逗号分隔）；`none` 表示全部交给独立宿主。默认本机能跑的都跑 |
| `WEKNORA_PLUGIN_HOST_API_URL` | app、plugin-host | 不在 app 本机运行的插件（远程插件、独立宿主上的插件）回调 Host API 的地址 |
| `WEKNORA_PLUGIN_HOST_KINDS` | plugin-host | 宿主运行的 kind，默认本机能跑的都跑 |
| `WEKNORA_PLUGIN_HOST_ADDR` | plugin-host | 监听地址，默认 `:8081` |
| `WEKNORA_PLUGIN_HOST_URL` | plugin-host | app 访问该宿主的地址，默认 `http://<主机名>:<端口>`；Helm 中为 Pod IP |
| `WEKNORA_PLUGIN_PYTHON` | 两者 | Python 插件的解释器，默认 `python3` |

独立宿主需要 Redis，且所有节点的 `SYSTEM_AES_KEY`（或 `JWT_SECRET`）必须一致。app 不运行某个 kind、又没有配置独立宿主时，该 kind 的插件在插件详情中显示为加载失败，并说明原因。

## 插件页面

插件可以在界面上加三种页面：

| 贡献点 | 出现在 | 默认最低角色 |
| --- | --- | --- |
| `pages` | 工具箱的一个标签页 | viewer |
| `settingsSections` | 设置窗口「插件」分组下的一节 | admin |
| `kbTabs` | 每个知识库的一个页签 | viewer |

页面是插件包 `ui/` 目录下的 HTML，WeKnora 通过 `/api/v1/plugin-ui/assets/...` 提供。

- **隔离**：页面在沙箱 iframe 中运行，没有同源权限，读不到 WeKnora 的登录状态和本地存储。严格的 CSP 禁止它访问网络。
- **通信**：页面只能经 [`@weknora/plugin-ui`](https://github.com/Tencent/WeKnora/tree/main/packages/plugin-ui) 桥与 WeKnora 通信，由 WeKnora 代发请求给插件后端，或者弹提示、确认框、跳转页面。
- **鉴权**：每次请求，WeKnora 都校验空间已启用该插件、用户满足页面的最低角色，并把用户角色一并交给插件后端。

数据源连接器和联网搜索还可以带一个**编辑页**，显示在自动生成的配置表单下方，用于表单难以表达的配置。编辑页读取并回填表单中的值，只有管理员可用。

## 事件与 Webhook

**事件**：插件在 `permissions.events` 中声明要订阅的事件，安装时由系统管理员审阅。

| 事件 | 时机 |
| --- | --- |
| `knowledge.ingested` | 文档处理完成、可被检索 |
| `knowledge.failed` | 文档处理最终失败 |
| `knowledge.deleted` | 文档被删除 |
| `chat.answered` | 一次回答完成，含问题与回答内容 |

- 只有启用了该插件的空间才会向它投递事件。
- 删除整个知识库时，其中的每个文档都会产生 `knowledge.deleted`；重启时被中断而置为失败的文档，在插件加载后产生 `knowledge.failed`。
- 事件经后台任务队列（有 Redis 时为 asynq）异步投递，至少一次。
- 插件返回可重试错误时，同一事件会以相同 ID 重新投递，最多 10 次。

**Webhook**：插件在 `contributes.webhooks` 中声明入站地址。

- 每个空间得到各自的秘密地址 `/api/v1/plugin-callbacks/...`，空间管理员可在「设置 → 插件」的「配置」中复制。
- 第三方系统调用该地址时，WeKnora 先校验地址、确认空间已启用插件，再把请求转给插件。
- 插件自行用空间配置里的密钥校验调用方。
- 设置 `APP_EXTERNAL_URL` 后，插件还能拿到完整地址，自动向第三方注册。
- 插件可以在收到 Webhook 后通过 Host API 立即同步自己的数据源，不必等待定时同步。

## 分块器

插件可以提供分块策略（`contributes.chunkers`）。空间启用插件后，知识库「分块设置」的策略下拉里会多出该插件的分块器，值为 `plugin:<插件 ID>/<分块器 ID>`。

- 插件只返回切分位置（按字符计的 `[start, end)` 区间，可附带标题路径 `contextHeader`），分块内容由 WeKnora 从文档中截取，插件无法改写或注入文本。
- 插件出错、超时（默认 2 分钟）、返回越界区间，或空间停用了插件时，自动改用内置的自动策略，入库不会因此失败。
- 开启父子分块时，插件只切父块，子块用内置策略切分，避免对每个父块都调用一次插件。
- 分块设置里的「测试分块」同样会调用插件，可先预览效果。

## 问答流程钩子

插件可以参与知识库问答流程的三个环节（`contributes.pipelineHooks`，在 `stages` 中声明参与哪些）：

| 环节 | 插件看到 | 插件能做 |
|---|---|---|
| `rewriteQuery` | 用户问题、WeKnora 改写后的检索问题 | 返回新的检索问题 |
| `filterResults` | 检索到的段落（ID、标题、内容、分数） | 返回要保留的段落 ID 及顺序：只能删除、调整顺序，不能添加 |
| `answer` | 生成完的回答 | 返回一段 Markdown 追加在回答之后（例如免责声明）；已发出的回答不能改 |

- 只作用于启用了该插件的空间，按插件注册顺序依次调用。
- 每次调用最多等 5 秒；出错、超时时跳过该插件，问答照常进行。
- 只对知识库问答（检索增强）生效，智能体模式不经过这些环节。

```yaml
contributes:
  pipelineHooks:
    - id: guard
      name: { zh-CN: 敏感内容过滤 }
      stages: [filterResults, answer]
```

## IM 渠道

插件可以接入新的 IM 平台（`contributes.imChannels`），与内置的飞书、Slack 等并列出现在智能体「IM 渠道」的平台列表里，凭证表单由插件的 `instanceSchema` 描述，密钥字段同样加密存储、脱敏显示。

- 目前只支持 Webhook 方式：平台把消息推到渠道的回调地址 `/api/v1/im/callback/<渠道 ID>`，WeKnora 把请求原样转给插件。插件校验签名、应答平台要求的握手或确认，并把其中的用户消息交回 WeKnora。
- WeKnora 用渠道绑定的智能体回答，再请插件把回复发回平台。回复一次性发送，不支持流式卡片。
- 需要长连接（WebSocket、长轮询）的平台暂不能以插件接入。

## 插件工具

插件可以给 Agent 提供工具。工具统一按 MCP 服务接入：

- 启用了插件的空间，会在 MCP 服务列表中看到插件提供的服务（只读），其中的工具与其他 MCP 工具一样，可在 Agent 中选用，并沿用相同的工具审批设置。
- 代码插件可以自己提供工具，无需单独部署 MCP 服务。WeKnora 调用工具时带上当前空间的插件配置，所以工具使用的是空间管理员在「设置 → 插件」中填写的账号。
- 插件可以为工具结果声明展示方式（表格、卡片、键值、Markdown、插件页面），对话中按此展示结构化结果；模型读取的仍是文本结果。

## 动态选项与账号连接

插件的配置表单可以在填写时向插件取数据：

- **动态选项**：下拉框的选项由插件实时给出，例如用填好的令牌列出项目。
  - 修改选项所依赖的字段后，列表会重新加载。
  - 编辑已保存的配置时，表单上没改动的密钥由服务端补齐，不会发回浏览器。
- **账号连接**：字段显示为「连接账号」按钮，点击后在弹窗中完成第三方的 OAuth 授权。
  - 令牌只保存在 WeKnora 服务端（加密）。每次调用插件时，WeKnora 换上有效的访问令牌，并在过期前自动刷新。
  - 连接只属于发起它的空间。

使用账号连接前，系统管理员需要：

1. 在第三方平台注册 OAuth 应用，回调地址填 `<WeKnora 地址>/api/v1/plugin-oauth/callback`。
2. 在「设置 → 插件管理」的插件详情中，把应用的 Client ID 和 Secret 填进平台配置。

回调地址取自 `APP_EXTERNAL_URL`；未设置时，使用浏览器访问 WeKnora 所用的地址。

桌面端在系统浏览器中打开授权页，回调地址是本机后端的 `http://127.0.0.1:<端口>/api/v1/plugin-oauth/callback`。端口默认每次启动随机分配；第三方平台要求回调地址完全一致时，先在桌面端设置中固定端口，再用该地址注册 OAuth 应用。授权完成后回到 WeKnora，表单会自动显示已连接。

## 开发插件

- **Go**：[pluginsdk](https://github.com/Tencent/WeKnora/tree/main/pluginsdk)，含协议定义、SDK、客户端和一致性测试工具 `weknora-plugin-conformance`。
- **Python**：[pluginsdk/python](https://github.com/Tencent/WeKnora/tree/main/pluginsdk/python)，Python 3.9+，只依赖标准库。
- **示例**：[examples/plugins](https://github.com/Tencent/WeKnora/tree/main/examples/plugins)：
  - `rss`：数据源连接器，Go；
  - `subtitles`：文档解析器，Go，使用 Host API；
  - `notebooks`：Jupyter 笔记本解析器，Python；
  - `links`：带三种页面的团队链接插件，Python；
  - `activity`：订阅事件、接收 Webhook 的空间动态插件，Python；
  - `jira`：Jira Cloud 问题同步与 Agent 工具（搜索、读取问题），支持 OAuth 授权或 API 令牌，带动态选项、增量同步与问题分诊技能，Go。

同一个插件既可以打包成 `host` 插件由 WeKnora 运行，也可以作为 `remote` 服务独立部署，代码不用改。

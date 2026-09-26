# 插件

插件是 WeKnora 扩展能力的打包与分发单元：模型厂商、数据源、联网搜索、文档解析、技能、MCP 服务等都可以由插件提供。内置能力本身也以内置插件的形式登记，和第三方插件共用一套目录与开关。

两类角色分工如下：

| | 系统管理员 | 空间管理员 |
| --- | --- | --- |
| 做什么 | 安装、升级、回滚、卸载插件，填写平台配置 | 在本空间启用或停用插件，填写空间配置 |
| 入口 | 「设置 → 插件管理」 | 「设置 → 插件」 |

安装后的插件对所有空间可见，但**默认停用**，由各空间管理员自行启用。停用插件后，它的集成不再出现在类型列表中、不能新建，已有实例照常工作。

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

### 远程插件

`runtime.type: remote` 的插件在安装时还需填写服务地址：

- 服务在内网时，需把地址加入 `SSRF_WHITELIST`。
- 安装后 WeKnora 生成一个签名密钥，**只显示这一次**。把它配置为服务的 `WEKNORA_PLUGIN_SECRET` 环境变量，服务据此确认请求来自 WeKnora。
- 插件详情中可以修改服务地址或轮换密钥。轮换后，服务换上新密钥之前的调用都会失败。
- 服务版本必须与安装的插件包一致，否则插件显示为异常、调用被拒绝。升级时同时升级服务与插件包。

## 运行代码插件

### 内嵌宿主（默认）

默认情况下，每个 app 进程自带插件宿主，在本机运行已安装的 `host` 插件：

- 插件进程只拿到 `WEKNORA_PLUGIN_*` 环境变量，看不到数据库密码等 WeKnora 密钥。
- 出网流量经宿主的出口代理，只放行插件在 `permissions.egress` 中声明的域名，内网地址一律拒绝。
- 宿主检查运行的进程与安装的包一致，健康检查失败时按退避重启，并在插件详情中显示为「异常」。
- Python 插件使用本机的 `python3`（可用 `WEKNORA_PLUGIN_PYTHON` 指定解释器）。Docker app 镜像已带 Python。

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

## 事件与 Webhook

**事件**：插件在 `permissions.events` 中声明要订阅的事件，安装时由系统管理员审阅。

| 事件 | 时机 |
| --- | --- |
| `knowledge.ingested` | 文档处理完成、可被检索 |
| `knowledge.failed` | 文档处理最终失败 |
| `knowledge.deleted` | 文档被删除 |
| `chat.answered` | 一次回答完成，含问题与回答内容 |

- 只有启用了该插件的空间才会向它投递事件。
- 事件经后台任务队列（有 Redis 时为 asynq）异步投递，至少一次。
- 插件返回可重试错误时，同一事件会以相同 ID 重新投递，最多 10 次。

**Webhook**：插件在 `contributes.webhooks` 中声明入站地址。

- 每个空间得到各自的秘密地址 `/api/v1/plugin-callbacks/...`，空间管理员可在「设置 → 插件」的「配置」中复制。
- 第三方系统调用该地址时，WeKnora 先校验地址、确认空间已启用插件，再把请求转给插件。
- 插件自行用空间配置里的密钥校验调用方。
- 设置 `APP_EXTERNAL_URL` 后，插件还能拿到完整地址，自动向第三方注册。

## 开发插件

- **Go**：[pluginsdk](https://github.com/Tencent/WeKnora/tree/main/pluginsdk)，含协议定义、SDK、客户端和一致性测试工具 `weknora-plugin-conformance`。
- **Python**：[pluginsdk/python](https://github.com/Tencent/WeKnora/tree/main/pluginsdk/python)，Python 3.9+，只依赖标准库。
- **示例**：[examples/plugins](https://github.com/Tencent/WeKnora/tree/main/examples/plugins)：
  - `rss`：数据源连接器，Go；
  - `subtitles`：文档解析器，Go，使用 Host API；
  - `notebooks`：Jupyter 笔记本解析器，Python；
  - `links`：带三种页面的团队链接插件，Python；
  - `activity`：订阅事件、接收 Webhook 的空间动态插件，Python。

同一个插件既可以打包成 `host` 插件由 WeKnora 运行，也可以作为 `remote` 服务独立部署，代码不用改。

# 图片与文件的对外访问

图片和文件在不同渠道中使用不同的访问方式。Web 控制台与嵌入挂件可携带凭证访问代理；IM 平台需要可公开加载的限时链接；API 客户端可按用途选择内部引用或外链。

接入时需同时确认引用形式、调用者权限和地址可达性。图片无法显示时，可按文末的排查表检查。

## 文件引用与访问方式 {#_1-四种形式}

知识库图片和附件保存在文件存储中。正文使用稳定的内部引用，响应或渲染时再按渠道转换为访问地址：

| 形式 | 地址示例 | 访问条件 | 有效期 |
| --- | --- | --- | --- |
| **内部引用** | `resource://<handle>` | 服务端解析的稳定句柄，不能直接作为 URL 访问 | — |
| **鉴权代理** | `/files`、`/api/v1/knowledge-bases/:id/files`、`/api/v1/embed/:channel_id/files`、消息级 `/api/v1/sessions/:id/messages/:message_id/files` | 带对应凭证的客户端（登录态 / KB 访问权 / Embed token） | 随凭证 |
| **能力短链** | `/r/<token>` | 任何拿到链接的人（**匿名可读**） | WeKnora 签发的 grant，2 小时 |
| **存储预签名** | 存储后端直接给的 http(s) 链接 | 任何拿到链接的人（**匿名可读**） | 由存储决定，MinIO 默认 24 小时 |

能力短链和存储预签名链接在有效期内允许持有者匿名读取文件。分享或记录这些链接时，应按文件本身的访问范围处理。

::: tip 默认访问方式
系统默认返回内部引用，由带凭证的客户端通过鉴权代理读取。外链需要公网可达的存储端点，或配置 `APP_EXTERNAL_URL` 后由 WeKnora 签发访问链接。默认 MinIO 地址 `minio:9000` 仅在容器网络内可达。
:::

## 按渠道访问文件 {#_2-各渠道分别怎么取}

```mermaid
flowchart TD
    R["正文里的 resource:// 引用"] --> Q{"哪个渠道"}
    Q -->|"Web 控制台"| W["前端改写为 /files 代理<br/>带 Bearer + X-Tenant-ID"]
    Q -->|"嵌入挂件"| E["/api/v1/embed/:channel_id/files<br/>带 Embed token"]
    Q -->|"IM 机器人"| I{"存储后端公网可达?"}
    I -->|"是"| IP["回退存储预签名 URL"]
    I -->|"否"| IE{"配了 APP_EXTERNAL_URL?"}
    IE -->|"是"| IR["改写为 APP_EXTERNAL_URL/r/token<br/>需 nginx 代理 /r/"]
    IE -->|"否"| IF["保留 resource:// 原样<br/>IM 无法加载图片, 日志记录告警"]
    Q -->|"REST API"| A{"resource_urls=public?"}
    A -->|"否 (默认)"| AH["返回 resource://<br/>客户端再调 /files 代理"]
    A -->|"是"| AP["返回预签名或 /r/token 外链"]
```

### Web 控制台

Web 前端将 `resource://` 和 `provider://` 引用转换为鉴权代理地址。普通资源使用 `/files`，请求携带 Bearer token 与 `X-Tenant-ID`；跨空间共享知识库使用 `/api/v1/knowledge-bases/:id/files`，按知识库访问权读取来源空间的文件，无需额外配置。

共享 Agent 或组织共享知识库回答中的图片，优先走 `/api/v1/sessions/:id/messages/:message_id/files?file_path=...`。服务端验证调用者能读到该消息、请求资源确实被消息引用，并重新校验资源所属知识库或共享 Agent 的授权。共享被撤销后，历史消息不继续授予资源访问。普通知识库浏览仍可用 KB 级代理，两者使用各自的上下文。

### IM 机器人 {#im-机器人-最常出问题的一条}

IM 平台无法携带 WeKnora 凭证，需要可公开访问的 HTTP(S) URL。发送消息前，系统根据存储与部署配置生成外链：

1. **存储后端本身公网可达**——对象存储用公网 endpoint，或把 `MINIO_ENDPOINT` 设成公网 host。此时回退到存储预签名 URL，不需要额外配置；
2. **配置 `APP_EXTERNAL_URL`**——引用被改写成 `<APP_EXTERNAL_URL>/r/<token>`，请求经 nginx 的 `location ^~ /r/` 反代回 app。官方前端镜像已内置该 location，自建反代必须补上，否则请求落进 SPA fallback 返回空白页。

默认 MinIO 内网部署和 `local` 存储需要配置 `APP_EXTERNAL_URL`。不满足外链条件时，系统保留原引用并记录告警，IM 平台无法直接显示该图片。已启用 IM 渠道但 `APP_EXTERNAL_URL` 为空时，启动阶段也会告警。

### 嵌入挂件

嵌入挂件使用渠道文件代理 `/api/v1/embed/:channel_id/files`。Embed token 确定渠道所属空间，服务端检查资源路径归属。嵌入渠道始终返回内部引用，`RESOURCE_URL_MODE=public` 和 `?resource_urls=public` 均不改变此行为，以确保读取经过渠道鉴权。

### REST API 与 SDK

API 默认返回 `resource://`，客户端通过鉴权代理获取文件。需要直接渲染外链时，可设置：

- 单次请求：`?resource_urls=public`
- 整个部署：`RESOURCE_URL_MODE=public`

单次参数优先于环境变量，因此把部署默认设成 `public` 之后仍可用 `?resource_urls=handle` 单独退回。支持该参数的接口、覆盖范围与安全边界见 [API 总览](../04-api/01-api-overview.md)的「文件引用形式」。

限定知识库范围的 API Key 使用 `public` 会返回 403，该类 Key 也不能访问通用 `/files` 代理。不具备外链条件时，响应保留 `resource://`，有相应权限的客户端可改用鉴权代理。

## 排查访问问题 {#_3-按症状排查}

| 现象 | 可能原因 | 处理方式 |
| --- | --- | --- |
| IM 中图片无法显示 | 未配 `APP_EXTERNAL_URL` 且存储不公网可达 | 配 `APP_EXTERNAL_URL`，确认 nginx 代理了 `/r/`；查 app 日志里 `rewriteStorageURLs no-op` 的 WARN |
| IM 图片链接能打开但返回空白页面 | nginx 缺 `location ^~ /r/`，请求落进 SPA fallback | 补上该 location（官方前端镜像已内置），见 [Web 前端](../05-clients/01-frontend.md) |
| `APP_EXTERNAL_URL` 配了内网地址或 `localhost` | IM 平台在公网侧，访问不到 | 换成 IM 平台可达的地址；本地开发用 ngrok / cloudflared / frp |
| API 返回的图片地址是 `resource://` | 默认就是内部引用 | 加 `?resource_urls=public`，或调 `/files` 代理 |
| 加了 `resource_urls=public` 仍返回 `resource://` | 部署不具备外链能力（如 `local` 存储且未配 `APP_EXTERNAL_URL`） | 补外链条件，或改用 `/files` 代理 |
| 加了 `resource_urls=public` 返回 403 | 用的是限定知识库的 API Key | 改用 `handle` 模式，或使用已获授权的 full-access Key |
| 嵌入挂件里图片不显示，但网页端正常 | 挂件走的是渠道代理，与主站凭证不同 | 确认挂件页面带着有效 Embed token；`resource_urls=public` 对嵌入渠道无效 |
| 共享回答里的图片 403/404 | 消息上下文缺失、资源未绑定或共享已撤销 | 使用消息级代理并检查当前共享权限；不要拼属主租户的 /files 地址 |
| 外链过一段时间失效 | 外链是限时的（grant 2 小时 / MinIO 预签名 24 小时） | 不要缓存外链本身，需要时重新取；同一文件在有效期内会复用同一链接 |
| 网页端图片 404，日志显示租户不匹配 | 跨租户共享库的图存在属主租户下 | 该场景应走 `/api/v1/knowledge-bases/:id/files`，确认前端拿到的是 KB 维度的代理地址 |

## 配置参考 {#_4-相关配置}

| 配置 | 作用 |
| --- | --- |
| `APP_EXTERNAL_URL` | IM 渠道图片外链的外部可达地址；`resource://` 改写成 `<APP_EXTERNAL_URL>/r/<token>` 的前提 |
| `RESOURCE_URL_MODE` | API 响应里文件引用的默认形式（`handle` / `public`） |
| `MINIO_ENDPOINT` 等存储 endpoint | 设为公网地址时，外链可由存储预签名提供，不必依赖 `APP_EXTERNAL_URL` |
| `SYSTEM_AES_KEY` | 建议配置：可复用 grant 行、稳定直链 URL，并降低读接口的写入压力 |

## 相关文档 {#_5-相关章节}

- [IM 集成](12-im-integration.md)：改写逻辑与启动告警
- [网页嵌入](13-embed-channel.md)：渠道鉴权与匿名会话
- [API 总览](../04-api/01-api-overview.md)：`resource_urls` 的完整语义
- [配置详解](../01-getting-started/04-configuration.md)：上述环境变量
- [Web 前端](../05-clients/01-frontend.md)：nginx 的 `/files` 与 `/r/` 代理

## 实现参考

- `frontend/src/utils/protectedFileAccess.ts`：Web 文件引用与代理路径。
- IM 消息发送前通过 `rewriteStorageURLs()` 转换外链。

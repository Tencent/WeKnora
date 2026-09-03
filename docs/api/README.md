# WeKnora API 文档

## 目录

- [概述](#概述)
- [最权威参考：Swagger UI](#最权威参考swagger-ui)
- [基础信息](#基础信息)
- [认证机制](#认证机制)
- [错误处理](#错误处理)
- [文件与图片引用（`resource://` 与直链）](#文件与图片引用resource-与直链)
- [API 概览](#api-概览)

## 概述

WeKnora 提供了一系列 RESTful API，用于创建和管理知识库、检索知识，以及进行基于知识的问答。本文档详细描述了这些 API 的使用方式。

## 最权威参考：Swagger UI

WeKnora 同时提供基于 OpenAPI 的 Swagger 文档。**启动服务后访问 `http://localhost:8080/swagger/index.html`**，可看到所有端点的完整参数、请求/响应 schema，并可直接在浏览器内试调——它随代码自动更新，是最准确的接口参考。

本目录下的 markdown 文档提供更易读的示例与场景说明，与 swagger 同步维护；当二者出现差异时，以 swagger 为准。

> Swagger UI 仅在非 release 模式（`GIN_MODE != release`）下挂载；生产部署默认关闭。

## 基础信息

- **基础 URL**: `/api/v1`
- **响应格式**: JSON
- **认证方式**: API Key

## 认证机制

所有 API 请求需要在 HTTP 请求头中包含 `X-API-Key` 进行身份认证：

```
X-API-Key: your_api_key
```

为便于问题追踪和调试，建议每个请求的 HTTP 请求头中添加 `X-Request-ID`：

```
X-Request-ID: unique_request_id
```

### 获取 API Key

在 web 页面完成账户注册后，请前往账户信息页面获取您的 API Key。

请妥善保管您的 API Key，避免泄露。API Key 代表您的账户身份，拥有完整的 API 访问权限。

## 错误处理

所有 API 使用标准的 HTTP 状态码表示请求状态，并返回统一的错误响应格式：

```json
{
  "success": false,
  "error": {
    "code": "错误代码",
    "message": "错误信息",
    "details": "错误详情"
  }
}
```

## 文件与图片引用（`resource://` 与直链）

响应里的图片、图表、附件默认以内部引用 `resource://<handle>` 返回，例如问答答案中的
`![示意图](resource://xifDo7NTSL300Lp1goVutw)`。这类引用不能被浏览器直接加载，客户端需要再
调用带鉴权的 `GET /files?file_path=<引用>` 代理去取字节流。

如果你在把 WeKnora 集成进自己的 App，可以让服务端直接返回**可加载的 http(s) 直链**，省掉这一次
额外请求：

| 方式 | 用法 | 生效范围 |
|------|------|----------|
| 单次请求 | 在 URL 上加 `?resource_urls=public` | 仅该次请求 |
| 整个部署 | 环境变量 `RESOURCE_URL_MODE=public` | 所有未显式传参的请求 |

`resource_urls` 取值为 `handle`（默认，保持内部引用）或 `public`（返回直链）；传其它值返回
`400`。单次请求的参数优先于环境变量，因此把部署默认设成 `public` 后，仍可用
`?resource_urls=handle` 单独退回。

支持该参数的接口：

- `POST /api/v1/knowledge-chat/{session_id}`（SSE）
- `POST /api/v1/agent-chat/{session_id}`（SSE）
- `GET /api/v1/sessions/continue-stream/{session_id}`（SSE）
- `GET /api/v1/messages/{session_id}/load`
- `POST /api/v1/knowledge-search`
- `POST /api/v1/knowledge-bases/{id}/hybrid-search`（兼容 GET）

改写覆盖答案正文、检索结果 `content` / `image_info`、`knowledge_references`、Agent 执行步骤与工具结果，以及消息
上的图片附件。流式回答里跨两个 chunk 被截断的引用会先缓冲再改写，客户端拿到的始终是完整链接。

### 注意事项

- **需要外链能力。** 直链由存储后端预签名，或由 `APP_EXTERNAL_URL` + `/r/<token>` 提供。二者都不
  可用时（例如 local 存储且未设 `APP_EXTERNAL_URL`），该引用**保持 `resource://` 原样**，客户端
  仍可回退到 `/files` 代理。详见 `.env.example` 中的 `APP_EXTERNAL_URL` 说明。
- **直链是限时匿名可读的**（WeKnora 签发的 grant 2 小时，MinIO 预签名 24 小时）。任何拿到链接的
  人在过期前都能读取该文件，请勿写入日志或转发给不应看到该文件的一方。
- **嵌入式（embed）渠道不支持该参数。** 其访客是匿名的，`/api/v1/embed/...` 下的接口会强制使用
  `handle`（即使传了 `?resource_urls=public`、或部署默认是 `public`），图片仍走渠道维度的鉴权代理。
- **限定知识库的 API Key 不能使用 `public`**，返回 `403`。这类 Key 本身也被拒绝访问 `/files`
  代理，若能拿到匿名直链等于绕过同一道限制。改用 `handle` 即可正常调用。
- **同一文件的直链会在有效期内复用**：重复请求不会反复签发凭证，也不会每次都拿到不同的 URL，客户端
  和 CDN 的缓存因此可以命中。凭证被吊销或过期后链接立即失效。

## API 概览

WeKnora API 按功能分为以下几类：

| 分类 | 描述 | 文档链接 |
|------|------|----------|
| 认证管理 | 用户注册、登录、令牌管理；OIDC 流程 | [auth.md](./auth.md) · [OIDC认证调用流程.md](../OIDC认证调用流程.md) |
| 空间管理 | 创建和管理空间账户 | [tenant.md](./tenant.md) |
| 知识库管理 | 创建、查询和管理知识库 | [knowledge-base.md](./knowledge-base.md) |
| 知识管理 | 上传、检索和管理知识内容 | [knowledge.md](./knowledge.md) |
| 模型管理 | 配置和管理各种AI模型 | [model.md](./model.md) |
| 分块管理 | 管理知识的分块内容 | [chunk.md](./chunk.md) |
| 标签管理 | 管理知识库的标签分类 | [tag.md](./tag.md) |
| FAQ管理 | 管理FAQ问答对 | [faq.md](./faq.md) |
| 智能体管理 | 创建和管理自定义智能体 | [agent.md](./agent.md) |
| 会话管理 | 创建和管理对话会话 | [session.md](./session.md) |
| 知识搜索 | 在知识库中搜索内容 | [knowledge-search.md](./knowledge-search.md) |
| 聊天功能 | 基于知识库和 Agent 进行问答 | [chat.md](./chat.md) |
| 消息管理 | 获取和管理对话消息 | [message.md](./message.md) |
| 评估功能 | 评估模型性能 | [evaluation.md](./evaluation.md) |
| 初始化管理 | 知识库模型配置与 Ollama 管理 | [initialization.md](./initialization.md) |
| 系统管理 | 系统信息、解析引擎、存储引擎 | [system.md](./system.md) |
| MCP 服务 | MCP 工具服务管理 | [mcp-service.md](./mcp-service.md) |
| 组织管理 | 组织、成员、知识库/智能体共享 | [organization.md](./organization.md) |
| Skills | 预装与已安装的智能体技能、技能环境变量 | [skill.md](./skill.md) |
| 网络搜索 | 网络搜索服务商 | [web-search.md](./web-search.md) |
| 向量存储 | 向量数据库连接管理 | [vector-store.md](./vector-store.md) |
| 存储后端 | 对象/文件存储实例（多实例）管理 | [storage-backend.md](./storage-backend.md) |
| IM 渠道 | 企业微信 / 飞书 / Slack 等 IM 平台对接，含渠道 CRUD 与回调 | [../IM集成开发文档.md](../IM集成开发文档.md) |
| 数据源导入 | 飞书 / 企微 / Notion / Confluence 等外部数据源接入与同步 | [../数据源导入开发文档.md](../数据源导入开发文档.md) |

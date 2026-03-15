# IM 集成开发文档

WeKnora 的 IM 集成模块将企业即时通讯平台（企业微信、飞书）接入 WeKnora 知识问答管道，支持在 IM 中直接向 AI 提问并获得实时流式回答。

## 目录

- [快速接入指南](#快速接入指南)
  - [企业微信接入](#企业微信接入)
  - [飞书接入](#飞书接入)
- [配置参考](#配置参考)
- [架构总览](#架构总览)
- [核心概念](#核心概念)
- [数据流](#数据流)
- [接口定义](#接口定义)
- [平台适配器详解](#平台适配器详解)
  - [企业微信 (WeCom)](#企业微信-wecom)
  - [飞书 (Feishu)](#飞书-feishu)
- [关键参数与阈值](#关键参数与阈值)
- [错误处理](#错误处理)
- [扩展新平台](#扩展新平台)

---

## 快速接入指南

### 企业微信接入

企业微信提供两种接入模式，根据你的应用类型选择：

#### 方式一：WebSocket 模式（智能机器人，推荐）

> 无需公网域名，适合快速验证和内网部署。

**第一步：创建智能机器人**

1. 登录 [企业微信工作台]（确认已升级到最新版企业微信） → **智能机器人** → **创建机器人** → **手动创建** → **切换API模式创建** → **选择“使用长连接”**
2. 创建完成后，在机器人详情页获取：
   - **BotID** — 机器人唯一标识
   - **BotSecret** — 机器人密钥（点击重置可重新生成）

**第二步：配置 WeKnora**

编辑 `config/config.yaml`：

```yaml
im:
  wecom:
    enabled: true
    mode: "websocket"                    # 使用 WebSocket 长连接
    output_mode: "stream"                # 流式输出（推荐）
    tenant_id: ["你的租户ID"]              # WeKnora 租户 ID
    knowledge_base_ids: ["你的知识库ID"]   # 关联的知识库
    bot_id: "${WECOM_BOT_ID}"            # 机器人 ID
    bot_secret: "${WECOM_BOT_SECRET}"    # 机器人 Secret
```

**第三步：启动服务**

启动 WeKnora 后，程序会自动建立到企业微信的 WebSocket 长连接（`wss://openws.work.weixin.qq.com`），无需额外配置回调地址。日志中出现以下内容表示连接成功：

```
[IM] WeCom WebSocket connecting (bot_id=xxx)...
```

此时在企业微信中给机器人发消息即可收到 AI 回复。

---

#### 方式二：Webhook 模式（自建应用）

> 需要公网可达的回调地址，适合已有自建应用的场景。

**第一步：创建自建应用**

1. 登录 [企业微信管理后台](https://work.weixin.qq.com/) → **应用管理** → **自建** → **创建应用**
2. 记录以下信息：
   - **CorpID** — 在 **我的企业** → **企业信息** 页面底部
   - **AgentID** — 应用详情页中的 AgentId（整数）
   - **Secret** — 应用详情页中的 Secret

**第二步：配置接收消息**

1. 在应用详情页 → **接收消息** → **设置 API 接收**
2. 填写：
   - **URL**：`https://你的域名/api/v1/im/callback/wecom`
   - **Token**：自定义或随机生成（记录下来）
   - **EncodingAESKey**：点击随机生成（记录下来）
3. 点击保存，企业微信会发送 GET 验证请求，WeKnora 需先启动以响应验证

**第三步：配置 WeKnora**

```yaml
im:
  wecom:
    enabled: true
    mode: "webhook"                      # 使用 Webhook 回调
    output_mode: "stream"
    tenant_id: ["你的租户ID"]
    knowledge_base_ids: ["你的知识库ID"]
    corp_id: "${WECOM_CORP_ID}"          # 企业 ID
    agent_secret: "${WECOM_AGENT_SECRET}" # 应用 Secret
    token: "${WECOM_TOKEN}"              # 接收消息 Token
    encoding_aes_key: "${WECOM_AES_KEY}" # 接收消息 EncodingAESKey
    corp_agent_id: 1000001               # 应用 AgentID（整数）
```

**第四步：配置可信域名（可选）**

如需在群聊中使用，在应用详情页 → **网页授权及 JS-SDK** 中添加可信域名。

---

### 飞书接入

飞书同样提供两种模式，WebSocket 模式配置更简单。

#### 方式一：WebSocket 模式（推荐）

> 无需公网域名，无需配置事件加密。

**第一步：创建飞书应用**

1. 登录 [飞书开放平台](https://open.feishu.cn/) → **开发者后台** → **创建企业自建应用**
2. 在 **凭证与基础信息** 页获取：
   - **App ID**
   - **App Secret**

**第二步：开通权限与事件**

1. **添加应用能力**：在应用详情页 → **添加应用能力** → 添加 **机器人** 能力
2. **配置权限**：在 **权限管理** 中搜索并开通以下权限：
   - `im:message` — 获取与发送单聊、群组消息（读写权限）
   - `im:message.group_at_msg` — 接收群聊中 @ 机器人消息
   - `im:chat` — 获取群组信息（只读权限，群聊场景需要）
   - `cardkit:card` — 创建和更新卡片（**流式输出必需**）
3. **配置事件订阅**：
   - 在 **事件与回调** → **事件配置** 中，选择请求方式为 **使用长连接接收事件**
   - 添加事件 `im.message.receive_v1`（接收消息）

**第三步：发布应用**

在 **版本管理与发布** 中创建版本并提交审核。审核通过后用户才能与机器人交互。

**第四步：配置 WeKnora**

```yaml
im:
  feishu:
    enabled: true
    mode: "websocket"                    # 使用长连接
    output_mode: "stream"                # 流式输出（推荐，需开启 cardkit:card 权限）
    tenant_id: ["你的租户ID"]
    knowledge_base_ids: ["你的知识库ID"]
    app_id: "${FEISHU_APP_ID}"           # App ID
    app_secret: "${FEISHU_APP_SECRET}"   # App Secret
    # WebSocket 模式无需 verification_token 和 encrypt_key
```

启动后日志出现以下内容表示连接成功：

```
[IM] Feishu WebSocket connecting (app_id=xxx)...
```

---

#### 方式二：Webhook 模式

> 需要公网可达的回调地址。

**前置步骤**同上（创建应用、开通权限），额外需要：

1. **配置事件订阅**：
   - 在 **事件与回调** → **事件配置** 中，选择请求方式为 **将事件发送到开发者服务器**
   - **请求地址**：`https://你的域名/api/v1/im/callback/feishu`
   - 记录页面上的 **Encrypt Key** 和 **Verification Token**
   - 添加事件 `im.message.receive_v1`
2. 点击保存时飞书会发送 URL 验证请求（challenge），WeKnora 需先启动以响应

```yaml
im:
  feishu:
    enabled: true
    mode: "webhook"
    output_mode: "stream"
    tenant_id: ["你的租户ID"]
    knowledge_base_ids: ["你的知识库ID"]
    app_id: "${FEISHU_APP_ID}"
    app_secret: "${FEISHU_APP_SECRET}"
    verification_token: "${FEISHU_TOKEN}"   # 事件订阅页面的 Verification Token
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"    # 事件订阅页面的 Encrypt Key
```

---

### 通用配置项说明

| 配置项 | 类型 | 说明 |
|--------|------|------|
| `enabled` | bool | 是否启用该平台 |
| `tenant_id` | uint64 | 关联的 WeKnora 租户 ID，消息处理在该租户上下文中执行 |
| `agent_id` | string | 指定自定义 Agent（可选，为空则使用默认知识库问答） |
| `knowledge_base_ids` | []string | 默认关联的知识库 ID 列表 |
| `mode` | string | 连接模式：`"websocket"` (长连接) 或 `"webhook"` (HTTP 回调) |
| `output_mode` | string | 输出模式：`"stream"` (实时流式) 或 `"full"` (等待完整回答后一次发送) |

### 模式选择指南

| | Webhook | WebSocket |
|---|---------|-----------|
| 网络要求 | 需要公网可达的 HTTPS 回调地址 | 仅需出站网络，无需公网 |
| 适用场景 | 已有域名和证书、自建应用 | 快速部署、内网环境、PoC 验证 |
| 连接管理 | 无状态，平台推送 | 自动维护长连接、心跳、断线重连 |
| 配置复杂度 | 需要配置回调 URL、加密密钥 | 只需 App ID/Secret |
| 流式输出 | 支持 | 支持 |

> **建议：** 优先使用 WebSocket 模式，部署更简单，不依赖公网域名。

---

## 配置参考

完整的 `config/config.yaml` 中 `im` 段配置：

```yaml
im:
  wecom:
    enabled: true                           # 是否启用企业微信
    tenant_id: 10000                        # 对应的 WeKnora 租户 ID
    agent_id: ""                            # 指定自定义 Agent (可选)
    knowledge_base_ids: []                  # 默认知识库 ID 列表
    mode: "websocket"                       # 连接模式: "webhook" | "websocket"
    output_mode: "stream"                   # 输出模式: "stream" (流式) | "full" (完整)

    # ── Webhook 模式配置 (自建应用) ──
    corp_id: "${WECOM_CORP_ID}"             # 企业 ID
    agent_secret: "${WECOM_AGENT_SECRET}"   # 应用 Secret
    token: "${WECOM_TOKEN}"                 # 接收消息的 Token
    encoding_aes_key: "${WECOM_AES_KEY}"    # 接收消息的 EncodingAESKey
    corp_agent_id: 1000001                  # 应用 AgentID（整数）

    # ── WebSocket 模式配置 (智能机器人) ──
    bot_id: "${WECOM_BOT_ID}"              # 机器人 ID
    bot_secret: "${WECOM_BOT_SECRET}"      # 机器人 Secret

  feishu:
    enabled: true                           # 是否启用飞书
    tenant_id: 10000
    agent_id: ""
    knowledge_base_ids: []
    mode: "websocket"                       # "websocket" | "webhook"
    output_mode: "stream"                   # "stream" | "full"
    app_id: "${FEISHU_APP_ID}"             # 应用 App ID
    app_secret: "${FEISHU_APP_SECRET}"     # 应用 App Secret
    verification_token: "${FEISHU_TOKEN}"  # 事件验证 Token (仅 Webhook 模式)
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"   # 加密密钥 (仅 Webhook 模式)
```

> **提示：** 配置值支持环境变量引用（`${VAR_NAME}` 语法），建议将密钥类配置通过环境变量注入，避免明文提交到代码仓库。

---

## 架构总览

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          IM 集成架构                                    │
│                                                                         │
│   ┌──────────┐    ┌──────────┐                                          │
│   │ 企业微信  │    │   飞书    │    IM 平台层                             │
│   └────┬─────┘    └────┬─────┘                                          │
│        │ Webhook/WS    │ Webhook/WS                                     │
│   ─────┼───────────────┼──────────────────────────────────────────────   │
│        ▼               ▼                                                │
│   ┌──────────┐    ┌──────────┐                                          │
│   │  WeCom   │    │  Feishu  │    平台适配器层 (im.Adapter)              │
│   │ Adapter  │    │ Adapter  │    · 消息解密、解析                       │
│   └────┬─────┘    └────┬─────┘    · 签名验证                            │
│        │               │          · 回复发送、流式推送                    │
│   ─────┼───────────────┼──────────────────────────────────────────────   │
│        └───────┬───────┘                                                │
│                ▼                                                        │
│        ┌──────────────┐                                                 │
│        │  im.Service  │           服务编排层                             │
│        │              │           · 消息去重 (MessageID + TTL)           │
│        │              │           · 会话映射 (ChannelSession)            │
│        │              │           · 流式/全量路由                        │
│        └──────┬───────┘           · QA 管道调度                          │
│               │                                                         │
│   ────────────┼─────────────────────────────────────────────────────    │
│               ▼                                                         │
│   ┌─────────────────────────────────────┐                               │
│   │    WeKnora Core (QA Pipeline)       │    核心层                     │
│   │  SessionService · MessageService    │                               │
│   │  TenantService  · AgentService      │                               │
│   └─────────────────────────────────────┘                               │
└─────────────────────────────────────────────────────────────────────────┘
```

**设计模式：**

| 模式 | 用途 |
|------|------|
| Adapter Pattern | 统一不同 IM 平台的差异，每个平台实现 `im.Adapter` 接口 |
| Strategy Pattern | `StreamSender` 可选接口，让平台按需支持流式输出 |
| Event-Driven | 通过 `EventBus` 解耦 QA 管道与 IM 输出，支持实时块推送 |

---

## 核心概念

### IncomingMessage — 统一入站消息

所有平台的消息在解密、解析后被归一化为 `IncomingMessage`，抹平平台差异：

```go
type IncomingMessage struct {
    Platform  Platform          // "wecom" | "feishu"
    UserID    string            // 平台用户标识
    UserName  string            // 显示名 (可选)
    ChatID    string            // 群聊 ID (私聊为空)
    ChatType  ChatType          // "direct" | "group"
    Content   string            // 纯文本内容
    MessageID string            // 平台消息 ID (用于去重)
    Extra     map[string]string // 平台特有字段
}
```

### ReplyMessage — 统一出站回复

```go
type ReplyMessage struct {
    Content    string            // Markdown 文本
    IsStreaming bool             // 是否为流式块
    IsFinal    bool             // 是否为最后一块
    Extra      map[string]string // 平台特有字段
}
```

### ChannelSession — 会话映射

将 IM 渠道 (平台 + 用户 + 群聊) 映射到 WeKnora 会话，实现对话上下文持续性：

```
(Platform, UserID, ChatID, TenantID)  →  SessionID
```

首次交互自动创建，后续消息复用同一会话。存储于 `im_channel_sessions` 表。

---

## 数据流

### 完整消息处理流程

```
用户在 IM 中发送消息
        │
        ▼
┌─ HTTP Handler ──────────────────────────────────┐
│  1. 签名验证 (VerifyCallback)                    │
│  2. URL 验证处理 (HandleURLVerification)          │
│  3. 解密 + 解析 → IncomingMessage (ParseCallback) │
│  4. 立即返回 HTTP 200 (异步处理)                   │
└──────────────────────────┬──────────────────────-┘
                           │ goroutine
                           ▼
┌─ im.Service ────────────────────────────────────┐
│  1. 去重检查 (MessageID, 5 分钟 TTL)             │
│  2. 内容长度校验 (≤ 4096 字符)                    │
│  3. 解析/创建 ChannelSession                     │
│  4. 获取 WeKnora Session                         │
│  5. 解析自定义 Agent (可选)                       │
│  6. 判断流式/全量模式                             │
└───────────┬─────────────────────┬───────────────┘
            │                     │
     流式模式 ▼              全量模式 ▼
┌────────────────────┐  ┌─────────────────────┐
│ handleMessageStream│  │ runQA (阻塞收集完整  │
│                    │  │ 回答后一次性发送)     │
│ · StartStream      │  └─────────────────────┘
│ · EventBus 订阅    │
│ · 300ms 批量刷新   │
│ · SendStreamChunk  │
│ · EndStream        │
└────────────────────┘
            │
            ▼
    消息持久化 (user + assistant)
```

### 流式输出机制

流式模式通过 `EventBus` 实时收集 QA 管道产生的内容块，以 **300ms 间隔批量推送**，在延迟与 API 限频之间取得平衡：

```
QA 管道 ──chunk──chunk──chunk──▶ EventBus
                                    │
                              每 300ms 刷新
                                    │
                        ┌───────────▼───────────┐
                        │ 累积内容 → 完整替换推送 │
                        │ (非增量，每次发送全文)   │
                        └───────────────────────┘
```

**关键细节：**
- `<think>...</think>` 块在流式输出中被过滤，不展示给用户
- 流式初始化失败时自动降级到全量模式 (`fallbackNonStream`)
- 完整内容（含 thinking）持久化到数据库，确保历史完整

---

## 接口定义

### im.Adapter — 平台适配器 (必须实现)

```go
type Adapter interface {
    Platform() Platform
    VerifyCallback(c *gin.Context) error
    ParseCallback(c *gin.Context) (*IncomingMessage, error)
    SendReply(ctx context.Context, incoming *IncomingMessage, reply *ReplyMessage) error
    HandleURLVerification(c *gin.Context) bool
}
```

| 方法 | 职责 |
|------|------|
| `Platform()` | 返回平台标识，用于路由和注册 |
| `VerifyCallback()` | 验证回调请求的签名/Token |
| `ParseCallback()` | 解密并解析回调为 `IncomingMessage`，非消息事件返回 `nil` |
| `SendReply()` | 通过平台 API 发送完整回复 |
| `HandleURLVerification()` | 处理平台初始 URL 验证（首次配置时调用） |

### im.StreamSender — 流式推送 (可选实现)

```go
type StreamSender interface {
    StartStream(ctx context.Context, incoming *IncomingMessage) (streamID string, err error)
    SendStreamChunk(ctx context.Context, incoming *IncomingMessage, streamID string, content string) error
    EndStream(ctx context.Context, incoming *IncomingMessage, streamID string) error
}
```

实现此接口后，Service 会自动路由到流式模式。可通过配置 `output_mode: "full"` 强制关闭。

---

## 平台适配器详解

### 企业微信 (WeCom)

提供两种连接模式，对应两套适配器实现：

#### Webhook 模式 (`WebhookAdapter`)

适用于**自建应用**，需要公网可访问的回调地址。

```
企业微信服务器 ──HTTP POST──▶ /api/v1/im/callback/wecom
                                      │
                              解密 (AES-128-CBC)
                              解析 XML → IncomingMessage
                                      │
                              处理完成后调用 WeCom REST API 回复
```

- **加密方案：** AES-128-CBC，Key 由 `encoding_aes_key` Base64 解码得到，IV 为 Key 前 16 字节
- **消息格式：** `random(16) + msg_len(4) + message + corp_id`，PKCS#7 填充
- **签名验证：** SHA-1(`sort([token, timestamp, nonce, encrypt])`)，常量时间比较
- **回复方式：** 通过 `/cgi-bin/message/send` 接口发送 Markdown 消息

#### WebSocket 模式 (`WSAdapter` + `LongConnClient`)

适用于**智能客服机器人**，无需公网域名，由客户端主动建立 WebSocket 长连接。

```
LongConnClient ══WebSocket══▶ wss://openws.work.weixin.qq.com
       │
  1. 发送 aibot_subscribe (bot_id + secret)
  2. 接收 aibot_msg_callback 消息帧
  3. 通过 aibot_respond_msg 回复
  4. 每 30s 心跳保活 (ping/pong)
  5. 断连自动重连 (指数退避 1s → 30s)
```

- **认证：** Bot ID + Bot Secret
- **流式回复：** 通过 WebSocket 帧发送，`finish=true` 标记结束
- **容错：** 指数退避重连，无限重试

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/wecom/adapter.go` | Webhook 模式：回调解密、签名验证、REST API 回复、Token 缓存 |
| `internal/im/wecom/bot_adapter.go` | WebSocket 模式适配器壳，代理到 `LongConnClient` |
| `internal/im/wecom/longconn.go` | WebSocket 客户端：连接管理、心跳、帧协议、自动重连 |

---

### 飞书 (Feishu)

统一适配器同时支持 Webhook 和 WebSocket 模式，且原生实现 `StreamSender` 接口。

#### Webhook 模式

```
飞书服务器 ──HTTP POST──▶ /api/v1/im/callback/feishu
                                   │
                           解密 (AES-256-CBC，可选)
                           解析 JSON → IncomingMessage
                                   │
                           通过飞书 Open API 回复
```

- **加密方案：** AES-256-CBC，Key 为 `SHA-256(encrypt_key)`，IV 为密文前 16 字节
- **事件过滤：** 仅处理 `im.message.receive_v1` 事件，忽略其他事件类型
- **群消息处理：** 自动去除 `@_user_xxx` 提及前缀

#### WebSocket 模式

通过飞书官方 SDK (`github.com/larksuite/oapi-sdk-go`) 建立长连接，事件推送与 Webhook 等价，无需公网域名，内置自动重连。

#### 流式回复 (CardKit v1)

飞书的流式输出基于 **CardKit 卡片流式更新**，是官方推荐的最佳实践：

```
StartStream:
  1. POST /cardkit/v1/cards              → 创建卡片实体 (streaming_mode: true)
  2. POST /im/v1/messages                → 发送卡片消息到聊天

SendStreamChunk:
  3. PUT /cardkit/v1/cards/{id}/elements/{eid}/content  → 更新元素内容 (累积全文)

EndStream:
  4. PATCH /cardkit/v1/cards/{id}/settings  → 设置 streaming_mode: false
```

每次 `SendStreamChunk` 发送的是**累积全文**而非增量，由 `feishuStreamState` 跟踪完整内容和严格递增的 `sequence` 序号。

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/feishu/adapter.go` | 事件解析、CardKit 流式实现、Token 缓存、AES 解密 |
| `internal/im/feishu/longconn.go` | WebSocket 长连接（封装飞书 SDK） |

---

## 关键参数与阈值

| 参数 | 值 | 说明 |
|------|------|------|
| `qaTimeout` | 120s | QA 管道最大执行时间 |
| `dedupTTL` | 5 min | 消息去重 ID 保留时长 |
| `dedupCleanupInterval` | 1 min | 去重清理周期 |
| `maxContentLength` | 4096 | 消息最大长度 (rune)，超出截断 |
| `streamFlushInterval` | 300ms | 流式内容批量刷新间隔 |
| WeCom 心跳 | 30s | WebSocket 保活频率 |
| WeCom 重连退避 | 1s → 30s | 指数退避，上限 30 秒 |
| Token 缓存安全余量 | 5 min | Token 过期前提前刷新 |

---

## 错误处理

| 场景 | 处理策略 |
|------|---------|
| 流式初始化失败 | 自动降级到全量模式 |
| QA 管道异常 | 回复 "抱歉，处理您的问题时出现了异常，请稍后再试。" |
| QA 超时 (>120s) | 标记消息完成，回复超时提示 |
| 空回答 | 回复 "抱歉，我暂时无法回答这个问题。" |
| WebSocket 断连 | 指数退避自动重连 |
| 平台重试 | MessageID 去重，5 分钟内自动跳过 |

---

## API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET/POST | `/api/v1/im/callback/wecom` | 企业微信回调 (GET 用于 URL 验证) |
| POST | `/api/v1/im/callback/feishu` | 飞书回调 (URL 验证 + 消息事件) |

---

## 扩展新平台

接入新的 IM 平台只需 3 步：

### 1. 实现 `im.Adapter` 接口

在 `internal/im/<platform>/` 下创建适配器：

```go
package dingtalk

type Adapter struct { /* 平台配置 */ }

func (a *Adapter) Platform() im.Platform     { return "dingtalk" }
func (a *Adapter) VerifyCallback(c *gin.Context) error { /* 签名验证 */ }
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) { /* 解析消息 */ }
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error { /* 发送回复 */ }
func (a *Adapter) HandleURLVerification(c *gin.Context) bool { /* URL 验证 */ }
```

如需流式输出，额外实现 `im.StreamSender`。

### 2. 注册适配器

在 `internal/container/container.go` 的 `registerIMAdapters` 中初始化并注册：

```go
if cfg.IM.DingTalk.Enabled {
    adapter := dingtalk.NewAdapter(cfg.IM.DingTalk.AppKey, cfg.IM.DingTalk.AppSecret)
    imService.RegisterAdapter(adapter)
}
```

### 3. 添加回调路由

在 `internal/router/router.go` 中注册 HTTP 端点：

```go
im.POST("/callback/dingtalk", imHandler.DingTalkCallback)
```

Service 层 (`im.Service`) 不需要任何修改 —— 消息编排、会话管理、QA 调度、流式控制全部由 Service 统一处理。

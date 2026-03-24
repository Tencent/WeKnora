# IM 集成开发文档

WeKnora 的 IM 集成模块将企业即时通讯平台（企业微信、飞书、Slack、Telegram、钉钉）接入 WeKnora 知识问答管道，支持在 IM 中直接向 AI 提问并获得实时流式回答。

IM 渠道绑定到 Agent，一个 Agent 可接入多个 IM 渠道，所有配置通过前端 Agent 编辑器管理，存储在数据库中。

## 目录

- [快速接入指南](#快速接入指南)
  - [企业微信接入](#企业微信接入)
  - [飞书接入](#飞书接入)
  - [Slack 接入](#slack-接入)
  - [Telegram 接入](#telegram-接入)
  - [钉钉接入](#钉钉接入)
- [前端管理](#前端管理)
- [架构总览](#架构总览)
- [数据模型](#数据模型)
- [API 端点](#api-端点)
- [核心概念](#核心概念)
- [消息处理流程](#消息处理流程)
- [接口定义](#接口定义)
- [平台适配器详解](#平台适配器详解)
  - [企业微信 (WeCom)](#企业微信-wecom)
  - [飞书 (Feishu)](#飞书-feishu)
  - [Slack](#slack)
  - [Telegram](#telegram)
  - [钉钉 (DingTalk)](#钉钉-dingtalk)
- [斜杠指令系统](#斜杠指令系统)
- [QA 队列与限流](#qa-队列与限流)
- [流式输出机制](#流式输出机制)
- [文件消息处理](#文件消息处理)
- [关键参数与阈值](#关键参数与阈值)
- [错误处理](#错误处理)
- [扩展新平台](#扩展新平台)

---

## 快速接入指南

### 前置条件

- WeKnora 已部署并运行
- 已创建至少一个 Agent（自定义智能体）
- Agent 已配置好模型和知识库

### 企业微信接入

企业微信提供两种接入模式，根据你的应用类型选择：

#### 方式一：WebSocket 模式（智能机器人，推荐）

> 无需公网域名，适合快速验证和内网部署。

**第一步：创建智能机器人**

1. 登录 [企业微信工作台]（确认已升级到最新版企业微信） → **智能机器人** → **创建机器人** → **手动创建** → **切换API模式创建** → **选择"使用长连接"**
2. 创建完成后，在机器人详情页获取：
   - **BotID** — 机器人唯一标识
   - **BotSecret** — 机器人密钥（点击重置可重新生成）

**第二步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → 左侧导航选择 **IM 集成** 标签页
2. 点击 **添加渠道**
3. 填写配置：
   - **平台**：选择「企业微信」
   - **渠道名称**：自定义名称，方便辨识（如「客服机器人」）
   - **接入模式**：选择「WebSocket」
   - **输出模式**：选择「流式输出」（推荐）
   - **Bot ID**：填入从企业微信获取的 BotID
   - **Bot Secret**：填入从企业微信获取的 BotSecret
4. 点击保存

**第三步：验证**

保存后 WeKnora 会自动建立到企业微信的 WebSocket 长连接。日志中出现以下内容表示连接成功：

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

**第二步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** 标签页 → **添加渠道**
2. 填写配置：
   - **平台**：选择「企业微信」
   - **接入模式**：选择「Webhook」
   - **输出模式**：选择「流式输出」
   - **Corp ID**：企业 ID
   - **Agent Secret**：应用 Secret
   - **Token**：自定义或随机生成（记录下来）
   - **EncodingAESKey**：自定义或随机生成（记录下来）
   - **Corp Agent ID**：应用 AgentID（整数）
3. 保存后，渠道卡片上会显示**回调地址**，格式为 `https://你的域名/api/v1/im/callback/{channel_id}`
4. 复制该回调地址

**第三步：配置企业微信接收消息**

1. 在应用详情页 → **接收消息** → **设置 API 接收**
2. 填写：
   - **URL**：粘贴上一步复制的回调地址
   - **Token**：填入在 WeKnora 中设置的 Token
   - **EncodingAESKey**：填入在 WeKnora 中设置的 EncodingAESKey
3. 点击保存，企业微信会发送 GET 验证请求，WeKnora 会自动响应

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
2. **配置权限**：在 **权限管理** 中搜索并开通以下权限：你的应用 → 权限管理 → 批量导入，粘贴下面 JSON（原文内容不变）：
```json
{
  "scopes": {
    "tenant": [
      "aily:file:read",
      "aily:file:write",
      "application:application.app_message_stats.overview:readonly",
      "application:application:self_manage",
      "application:bot.menu:write",
      "cardkit:card:write",
      "contact:user.employee_id:readonly",
      "corehr:file:download",
      "docs:document.content:read",
      "event:ip_list",
      "im:chat",
      "im:chat.access_event.bot_p2p_chat:read",
      "im:chat.members:bot_access",
      "im:message",
      "im:message.group_at_msg:readonly",
      "im:message.group_msg",
      "im:message.p2p_msg:readonly",
      "im:message:readonly",
      "im:message:send_as_bot",
      "im:resource",
      "sheets:spreadsheet",
      "wiki:wiki:readonly"
    ],
    "user": [
      "aily:file:read",
      "aily:file:write",
      "im:chat.access_event.bot_p2p_chat:read"
    ]
  }
}
```
3. **配置事件订阅**：
   - 在 **事件与回调** → **事件配置** 中，选择请求方式为 **使用长连接接收事件**
   - 添加事件 `im.message.receive_v1`（接收消息）

**第三步：发布应用**

在 **版本管理与发布** 中创建版本并提交审核。审核通过后用户才能与机器人交互。

**第四步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「飞书」
   - **接入模式**：选择「WebSocket」
   - **输出模式**：选择「流式输出」（需开启 cardkit:card 权限）
   - **App ID**：填入从飞书获取的 App ID
   - **App Secret**：填入从飞书获取的 App Secret
3. 保存

启动后日志出现以下内容表示连接成功：

```
[IM] Feishu WebSocket connecting (app_id=xxx)...
```

---

#### 方式二：Webhook 模式

> 需要公网可达的回调地址。

**前置步骤**同上（创建应用、开通权限），额外需要：

**第一步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「飞书」
   - **接入模式**：选择「Webhook」
   - **App ID** / **App Secret**
   - **Verification Token**：从飞书事件订阅页面获取
   - **Encrypt Key**：从飞书事件订阅页面获取
3. 保存后，复制渠道卡片上显示的**回调地址**

**第二步：配置飞书事件订阅**

1. 在 **事件与回调** → **事件配置** 中，选择请求方式为 **将事件发送到开发者服务器**
2. **请求地址**：粘贴从 WeKnora 复制的回调地址
3. 添加事件 `im.message.receive_v1`
4. 点击保存时飞书会发送 URL 验证请求（challenge），WeKnora 会自动响应

---

### Slack 接入

Slack 提供两种接入模式，推荐使用 WebSocket (Socket Mode) 模式，无需公网域名。

#### 方式一：WebSocket 模式（Socket Mode，推荐）

> 无需公网域名，适合快速验证和内网部署。

**第一步：创建 Slack App**

1. 登录 [Slack API](https://api.slack.com/apps) → **Create New App** → **From scratch**
2. 填写 App Name 并选择要安装的 Workspace。

**第二步：生成 App-Level Token**

1. 在应用详情页左侧导航栏选择 **Basic Information**。
2. 滚动到 **App-Level Tokens** 区域，点击 **Generate Token and Scopes**。
3. 填写 Token Name，添加 `connections:write` scope。
4. 点击 Generate，复制生成的 Token（以 `xapp-` 开头），这就是 **App Token**。

**第三步：开启 Socket Mode**

1. 在左侧导航栏选择 **Socket Mode**。
2. 开启 **Enable Socket Mode** 开关。

**第四步：配置 Event Subscriptions**

1. 在左侧导航栏选择 **Event Subscriptions**。
2. 开启 **Enable Events** 开关。
3. 展开 **Subscribe to bot events**，添加以下事件：
   - `app_mention` (在频道中 @ 机器人)
   - `message.channels` (频道消息)
   - `message.groups` (私有频道消息)
   - `message.im` (私聊消息)
   - `message.mpim` (多人私聊消息)
4. 点击 **Save Changes**。

**第五步：配置权限 (OAuth & Permissions)**

1. 在左侧导航栏选择 **OAuth & Permissions**。
2. 滚动到 **Scopes** -> **Bot Token Scopes**，确保包含以下权限（添加事件时通常会自动添加）：
   - `app_mentions:read`
   - `channels:history`
   - `chat:write`
   - `groups:history`
   - `im:history`
   - `mpim:history`
   - `files:read` (用于接收文件)
3. 滚动到顶部，点击 **Install to Workspace**。
4. 授权后，复制 **Bot User OAuth Token**（以 `xoxb-` 开头），这就是 **Bot Token**。

**第六步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「Slack」
   - **接入模式**：选择「WebSocket」
   - **输出模式**：选择「流式输出」
   - **App Token**：填入以 `xapp-` 开头的 Token
   - **Bot Token**：填入以 `xoxb-` 开头的 Token
3. 保存

启动后日志出现以下内容表示连接成功：

```
[IM] Slack WebSocket connecting...
```

---

#### 方式二：Webhook 模式 (Events API)

> 需要公网可达的回调地址。

**第一步：创建 Slack App 并获取凭证**

1. 登录 [Slack API](https://api.slack.com/apps) 创建应用。
2. 在 **Basic Information** 页面，滚动到 **App Credentials** 区域，复制 **Signing Secret**。
3. 在 **OAuth & Permissions** 页面，配置 Bot Token Scopes（同上），安装到 Workspace，复制 **Bot User OAuth Token**（Bot Token）。

**第二步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「Slack」
   - **接入模式**：选择「Webhook」
   - **Bot Token**：填入以 `xoxb-` 开头的 Token
   - **Signing Secret**：填入 Signing Secret
3. 保存后，复制渠道卡片上显示的**回调地址**。

**第三步：配置 Event Subscriptions**

1. 在 Slack App 设置页左侧导航栏选择 **Event Subscriptions**。
2. 开启 **Enable Events** 开关。
3. 在 **Request URL** 中粘贴从 WeKnora 复制的回调地址。Slack 会发送一个 challenge 请求，WeKnora 会自动响应并验证通过。
4. 展开 **Subscribe to bot events**，添加需要的事件（同上）。
5. 点击 **Save Changes**。

---

### Telegram 接入

Telegram 提供两种接入模式，推荐使用 WebSocket（长轮询）模式，无需公网域名。

#### 方式一：WebSocket 模式（长轮询，推荐）

> 无需公网域名，适合快速验证和内网部署。

**第一步：创建 Telegram Bot**

1. 在 Telegram 中搜索 [@BotFather](https://t.me/BotFather) 并开始对话
2. 发送 `/newbot`，按提示填写 Bot 名称和用户名
3. 创建完成后获取 **Bot Token**（格式如 `123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11`）

**第二步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「Telegram」
   - **接入模式**：选择「WebSocket」
   - **输出模式**：选择「流式输出」（推荐）
   - **Bot Token**：填入从 BotFather 获取的 Token
3. 保存

启动后日志出现以下内容表示连接成功：

```
[IM] Telegram long polling connecting...
```

此时在 Telegram 中给 Bot 发送消息即可收到 AI 回复。

---

#### 方式二：Webhook 模式

> 需要公网可达的回调地址（HTTPS）。

**第一步：创建 Telegram Bot 并获取凭证**

同上，通过 BotFather 创建 Bot 并获取 Bot Token。

**第二步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「Telegram」
   - **接入模式**：选择「Webhook」
   - **Bot Token**：填入 Bot Token
   - **Secret Token**（可选）：自定义密钥，用于验证回调请求的 `X-Telegram-Bot-Api-Secret-Token` 头
3. 保存后，复制渠道卡片上显示的**回调地址**

**第三步：配置 Webhook**

通过 Telegram Bot API 设置 Webhook：

```bash
curl -X POST "https://api.telegram.org/bot<YOUR_BOT_TOKEN>/setWebhook" \
  -H "Content-Type: application/json" \
  -d '{"url": "<YOUR_CALLBACK_URL>", "secret_token": "<YOUR_SECRET_TOKEN>"}'
```

> 注意：Telegram Webhook 必须使用 HTTPS。

---

### 钉钉接入

钉钉提供两种接入模式，推荐使用 Stream 模式（WebSocket），无需公网域名。

#### 方式一：WebSocket 模式（Stream，推荐）

> 无需公网域名，适合快速验证和内网部署。

**第一步：创建钉钉机器人**

1. 登录 [钉钉开放平台](https://open-dev.dingtalk.com/) → **应用开发** → **创建应用**
2. 在应用详情页 → **添加应用能力** → 添加 **机器人** 能力
3. 在 **凭证与基础信息** 页获取：
   - **Client ID**（AppKey）
   - **Client Secret**（AppSecret）

**第二步：配置机器人**

1. 在应用详情页 → **机器人** → 配置消息接收模式为 **Stream 模式**
2.（可选）如需流式 AI 卡片效果，在 **互动卡片** 中创建一个 **AI 卡片模板**，记录 **Card Template ID**

**第三步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「钉钉」
   - **接入模式**：选择「WebSocket」
   - **输出模式**：选择「流式输出」（推荐）
   - **Client ID**：填入 AppKey
   - **Client Secret**：填入 AppSecret
   - **Card Template ID**（可选）：填入 AI 卡片模板 ID，启用后流式回复将以 AI 卡片形式展示
3. 保存

启动后日志出现以下内容表示连接成功：

```
[IM] DingTalk Stream connecting...
```

> **关于 AI 卡片**：配置 Card Template ID 后，流式回复将通过钉钉 AI 卡片实时展示打字效果；未配置时流式内容将在结束后一次性发送。

---

#### 方式二：Webhook 模式

> 需要公网可达的回调地址。

**第一步：创建钉钉机器人并获取凭证**

同上，创建应用并获取 Client ID 和 Client Secret。

**第二步：在 WeKnora 中添加 IM 渠道**

1. 进入 Agent 编辑器 → **IM 集成** → **添加渠道**
2. 填写配置：
   - **平台**：选择「钉钉」
   - **接入模式**：选择「Webhook」
   - **Client ID**：填入 AppKey
   - **Client Secret**：填入 AppSecret
   - **Card Template ID**（可选）：同上
3. 保存后，复制渠道卡片上显示的**回调地址**

**第三步：配置钉钉事件订阅**

1. 在应用详情页 → **机器人** → 配置消息接收模式为 **HTTP 模式**
2. **消息接收地址**：粘贴从 WeKnora 复制的回调地址
3. 钉钉会通过 HmacSHA256 签名验证回调请求

---

## 前端管理

IM 渠道在 Agent 编辑器的 **IM 集成** 标签页中管理（仅编辑模式可见，创建 Agent 时不显示）。

### 渠道列表

每个渠道以卡片形式展示，包含：
- **平台标识**：企业微信（绿色）/ 飞书（蓝色）/ Slack（紫色）/ Telegram（蓝色）/ 钉钉（蓝色）
- **渠道名称**：用户自定义
- **接入模式**：WebSocket / Webhook
- **输出模式**：流式输出 / 完整输出
- **启用开关**：可即时启用/停用渠道
- **回调地址**：Webhook 模式下显示，可一键复制
- **编辑/删除**：管理渠道配置

### 渠道操作

- **添加渠道**：选择平台 → 填写凭证 → 选择模式 → 保存
- **编辑渠道**：可修改名称、模式、输出模式和凭证（平台不可更改）
- **启用/停用**：通过开关即时切换，停用的渠道不会处理消息
- **删除渠道**：删除后不可恢复

---

## 架构总览

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                              IM 集成架构                                     │
│                                                                              │
│   ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐      │
│   │ 企业微信  │  │   飞书    │  │  Slack   │  │ Telegram │  │   钉钉   │  IM层│
│   └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘      │
│        │ WH/WS       │ WH/WS      │ WH/WS       │ WH/LP       │ WH/Stream  │
│   ─────┼─────────────┼────────────┼─────────────┼─────────────┼──────────   │
│        ▼             ▼            ▼             ▼             ▼              │
│   ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐      │
│   │  WeCom   │  │  Feishu  │  │  Slack   │  │ Telegram │  │ DingTalk │ 适配 │
│   │ Adapter  │  │ Adapter  │  │ Adapter  │  │ Adapter  │  │ Adapter  │ 器层 │
│   └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘      │
│        │             │             │              │             │             │
│   ─────┼─────────────┼─────────────┼──────────────┼─────────────┼────────   │
│        └─────────────┼─────────────┼──────────────┘─────────────┘            │
│                        ▼                                                     │
│   ┌──────────────────────────────────┐                                       │
│   │         im.Service               │     服务编排层                        │
│   │                                  │     · IM 渠道管理 (CRUD)              │
│   │  ┌────────────────────────────┐  │     · Adapter Factory (动态创建)      │
│   │  │ CommandRegistry            │  │     · 斜杠指令分发                    │
│   │  │ qaQueue (Worker Pool)      │  │     · QA 队列调度 (有界, 异步)        │
│   │  │ rateLimiter (滑动窗口)      │  │     · 滑动窗口限流                    │
│   │  │ processedMsgs (去重)       │  │     · 消息去重 (MessageID + TTL)      │
│   │  │ inflight (取消跟踪)         │  │     · 会话映射 (ChannelSession)       │
│   │  └────────────────────────────┘  │     · 流式/全量路由                    │
│   └──────────────┬───────────────────┘                                       │
│                  │                                                           │
│   ───────────────┼───────────────────────────────────────────────────────    │
│                  ▼                                                           │
│   ┌──────────────────────────────────────┐                                   │
│   │     WeKnora Core (QA Pipeline)       │     核心层                       │
│   │   SessionService · MessageService    │                                   │
│   │   TenantService  · AgentService      │                                   │
│   │   KnowledgeService (文件保存)         │                                   │
│   └──────────────────────────────────────┘                                   │
└──────────────────────────────────────────────────────────────────────────────┘
```

**设计模式：**

| 模式 | 用途 |
|------|------|
| Adapter Pattern | 统一不同 IM 平台的差异，每个平台实现 `im.Adapter` 接口 |
| Factory Pattern | 通过 `AdapterFactory` 从数据库渠道配置动态创建 Adapter 实例 |
| Strategy Pattern | `StreamSender`、`FileDownloader` 可选接口，按需实现 |
| Command Pattern | `Command` 接口 + `CommandRegistry` 实现可插拔的斜杠指令系统 |
| Producer-Consumer | `qaQueue` 有界队列 + Worker Pool，解耦消息接收与 QA 执行 |
| Event-Driven | 通过 `EventBus` 解耦 QA 管道与 IM 输出，支持实时块推送 |

---

## 数据模型

### im_channels 表

IM 渠道配置存储在 `im_channels` 表中，绑定到 Agent：

```sql
CREATE TABLE im_channels (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    agent_id          VARCHAR(36) NOT NULL,       -- 绑定的 Agent ID
    platform          VARCHAR(20) NOT NULL,       -- 'wecom' | 'feishu' | 'slack' | 'telegram' | 'dingtalk'
    name              VARCHAR(255) NOT NULL DEFAULT '',
    enabled           BOOLEAN NOT NULL DEFAULT true,
    mode              VARCHAR(20) NOT NULL DEFAULT 'websocket',  -- 'webhook' | 'websocket'
    output_mode       VARCHAR(20) NOT NULL DEFAULT 'stream',     -- 'stream' | 'full'
    knowledge_base_id VARCHAR(36),                -- 可选，绑定知识库以接收文件消息
    bot_identity      VARCHAR(255),               -- 计算字段，防止重复机器人
    credentials       JSONB NOT NULL DEFAULT '{}',               -- 平台凭证
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TIMESTAMPTZ
);
```

**credentials 字段结构：**

| 平台 | 模式 | 字段 |
|------|------|------|
| 企业微信 | WebSocket | `bot_id`, `bot_secret` |
| 企业微信 | Webhook | `corp_id`, `agent_secret`, `token`, `encoding_aes_key`, `corp_agent_id` |
| 飞书 | WebSocket | `app_id`, `app_secret` |
| 飞书 | Webhook | `app_id`, `app_secret`, `verification_token`, `encrypt_key` |
| Slack | WebSocket | `app_token`, `bot_token` |
| Slack | Webhook | `bot_token`, `signing_secret` |
| Telegram | WebSocket | `bot_token` |
| Telegram | Webhook | `bot_token`, `secret_token`（可选） |
| 钉钉 | WebSocket | `client_id`, `client_secret`, `card_template_id`（可选） |
| 钉钉 | Webhook | `client_id`, `client_secret`, `card_template_id`（可选） |

### im_channel_sessions 表

将 IM 渠道中的用户会话映射到 WeKnora 会话：

```
(im_channel_id, Platform, UserID, ChatID, TenantID)  →  SessionID
```

首次交互自动创建，后续消息复用同一会话。`/clear` 指令会软删除会话记录，下次消息重新创建。

---

## API 端点

### IM 渠道管理 API（需认证）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/agents/:id/im-channels` | 创建 IM 渠道 |
| GET | `/api/v1/agents/:id/im-channels` | 列出 Agent 的所有 IM 渠道 |
| PUT | `/api/v1/im-channels/:id` | 更新 IM 渠道 |
| DELETE | `/api/v1/im-channels/:id` | 删除 IM 渠道 |
| POST | `/api/v1/im-channels/:id/toggle` | 启用/停用 IM 渠道 |

### IM 回调端点（无需认证，平台签名验证）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET/POST | `/api/v1/im/callback/:channel_id` | 通用回调（根据 channel_id 自动路由到对应 Adapter） |

> Webhook 模式下，每个渠道有唯一的回调地址 `/api/v1/im/callback/{channel_id}`，在前端渠道卡片上可一键复制。回调路由注册在认证中间件**之前**，由平台签名验证保护。

---

## 核心概念

### IMChannel — IM 渠道

每个 IM 渠道代表一个 IM 平台机器人与 WeKnora Agent 的绑定关系。一个 Agent 可以绑定多个渠道（如同时接入企业微信、飞书和 Slack），同一平台也可以创建多个渠道（如不同的企业微信机器人）。

渠道有一个计算字段 `BotIdentity`，由平台类型、模式和核心凭证推导，用于防止同一机器人被重复创建。

渠道启动时，Service 通过 `AdapterFactory` 根据平台类型和凭证动态创建对应的 Adapter 实例。

### IncomingMessage — 统一入站消息

所有平台的消息在解密、解析后被归一化为 `IncomingMessage`，抹平平台差异：

```go
type IncomingMessage struct {
    Platform    Platform          // "wecom" | "feishu" | "slack" | "telegram" | "dingtalk"
    MessageType MessageType       // "text" | "file" | "image"
    UserID      string            // 平台用户标识
    UserName    string            // 显示名 (可选)
    ChatID      string            // 群聊 ID (私聊为空)
    ChatType    ChatType          // "direct" | "group"
    Content     string            // 纯文本内容
    MessageID   string            // 平台消息 ID (用于去重)
    FileKey     string            // 文件标识 (文件/图片消息)
    FileName    string            // 文件名 (文件/图片消息)
    FileSize    int64             // 文件大小 (字节)
    Extra       map[string]string // 平台特有字段 (如 req_id、aes_key)
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

将 IM 渠道 (渠道 ID + 用户 + 群聊) 映射到 WeKnora 会话，实现对话上下文持续性。首次交互自动创建，后续消息复用同一会话。并发创建通过唯一约束 + fallback 查询处理。存储于 `im_channel_sessions` 表。

---

## 消息处理流程

### 完整消息处理流程

```
用户在 IM 中发送消息
        │
        ▼
┌─ HTTP Handler / WebSocket 回调 ─────────────────┐
│  1. 根据 channel_id 查找渠道配置                   │
│  2. 获取对应 Adapter                              │
│  3. 签名验证 (VerifyCallback)                     │
│  4. URL 验证处理 (HandleURLVerification)           │
│  5. 解密 + 解析 → IncomingMessage (ParseCallback)  │
│  6. 立即返回 HTTP 200 (异步处理)                    │
└──────────────────────────┬──────────────────────-┘
                           │ goroutine
                           ▼
┌─ im.Service.HandleMessage ──────────────────────┐
│  1. 去重检查 (MessageID, 5 分钟 TTL)             │
│  2. 内容长度校验 (≤ 4096 rune，超出截断)           │
│  3. 斜杠指令检测 → 命中则分发到 CommandRegistry   │
│  4. 限流检查 (滑动窗口, 10次/60s)                 │
│  5. 从渠道配置获取 agent_id、tenant_id            │
│  6. 解析/创建 ChannelSession                     │
│  7. 获取 WeKnora Session                         │
│  8. 加载 Agent 配置（获取知识库、模型等信息）       │
│  9. 文件消息？→ 下载并保存到知识库                  │
│ 10. 提交到 qaQueue (有界队列, 异步执行)            │
└───────────┬─────────────────────────────────────┘
            │
            ▼
┌─ qaQueue Worker ────────────────────────────────┐
│  从队列取出请求，记录 inflight，判断流式/全量模式   │
└───────────┬─────────────────────┬───────────────┘
            │                     │
     流式模式 ▼              全量模式 ▼
┌────────────────────┐  ┌─────────────────────┐
│ handleMessageStream│  │ runQA (阻塞收集完整  │
│                    │  │ 回答后一次性发送)     │
│ · StartStream      │  └─────────────────────┘
│ · EventBus 订阅    │
│ · 300ms 批量刷新   │
│ · 工具事件展示     │
│ · SendStreamChunk  │
│ · EndStream        │
└────────────────────┘
            │
            ▼
    消息持久化 (user + assistant)
```

### 渠道生命周期

```
渠道创建/更新 (前端 UI)
        │
        ▼
┌─ im.Service ──────────────────────────┐
│  1. 保存渠道配置到数据库                │
│  2. 如果渠道已启用：                    │
│     a. AdapterFactory 创建 Adapter     │
│     b. WebSocket 模式：建立长连接       │
│     c. Webhook 模式：注册回调处理       │
│  3. 维护 channels map (channel_id →    │
│     channelState{Channel, Adapter})    │
└────────────────────────────────────────┘

服务启动时：
  LoadAndStartChannels() → 从 DB 加载所有 enabled 的渠道 → 逐个 StartChannel()

渠道停用/删除时：
  StopChannel() → 取消 Adapter 上下文 → 从 map 移除
```

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

### im.StreamSender — 流式推送 (可选)

```go
type StreamSender interface {
    StartStream(ctx context.Context, incoming *IncomingMessage) (streamID string, err error)
    SendStreamChunk(ctx context.Context, incoming *IncomingMessage, streamID string, content string) error
    EndStream(ctx context.Context, incoming *IncomingMessage, streamID string) error
}
```

实现此接口后，Service 会自动路由到流式模式。渠道配置 `output_mode: "full"` 可强制关闭。

### im.FileDownloader — 文件下载 (可选)

```go
type FileDownloader interface {
    DownloadFile(ctx context.Context, msg *IncomingMessage) (io.ReadCloser, string, error)
}
```

实现此接口后，当用户发送文件/图片消息且渠道配置了 `knowledge_base_id` 时，Service 会自动下载文件并保存到指定知识库。

### im.AdapterFactory — 适配器工厂

```go
type AdapterFactory func(ctx context.Context, channel *IMChannel, msgHandler func(*IncomingMessage)) (Adapter, CancelFunc, error)
```

每个平台注册一个工厂函数，Service 在启动渠道时调用工厂创建 Adapter 实例。工厂函数根据渠道的 `mode` 和 `credentials` 决定创建哪种 Adapter。

---

## 平台适配器详解

### 企业微信 (WeCom)

提供两种连接模式，对应两套适配器实现：

#### Webhook 模式 (`WebhookAdapter`)

适用于**自建应用**，需要公网可访问的回调地址。

```
企业微信服务器 ──HTTP POST──▶ /api/v1/im/callback/{channel_id}
                                      │
                              解密 (AES-256-CBC)
                              解析 XML → IncomingMessage
                                      │
                              处理完成后调用 WeCom REST API 回复
```

- **加密方案：** AES-256-CBC，Key 由 `encoding_aes_key` Base64 解码得到（32 字节），IV 为 Key 前 16 字节
- **消息格式：** `random(16) + msg_len(4) + message + corp_id`，PKCS#7 填充
- **签名验证：** SHA-1(`sort([token, timestamp, nonce, encrypt])`)，常量时间比较
- **消息类型：** 支持 `text`（文本）和 `image`（图片，PicUrl 直接下载或 MediaId 临时素材 API）
- **群聊回复：** 优先尝试 `appchat/send` 群聊 API，失败时降级到私聊直发
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
- **消息类型：** `text`（文本）、`image`（图片）、`file`（文件）、`voice`（语音，服务端已转文本）、`mixed`（混合，文本 + 图片）、`event`（服务器事件）
- **文件解密：** 附件使用每消息独立 AES-256-CBC 密钥解密（IV 为密钥前 16 字节）
- **流式回复：** 通过 WebSocket 帧发送累积全文，`finish=true` 标记结束
- **容错：** 指数退避重连（基础 1s，上限 30s），读超时 = 3 × 心跳间隔（90s）

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/wecom/webhook_adapter.go` | Webhook 模式：回调解密、签名验证、REST API 回复、群聊发送、Token 缓存、文件下载 |
| `internal/im/wecom/ws_adapter.go` | WebSocket 模式适配器壳，代理到 `LongConnClient` |
| `internal/im/wecom/longconn.go` | WebSocket 客户端：连接管理、心跳、帧协议、自动重连、多消息类型解析、文件解密 |

---

### 飞书 (Feishu)

统一适配器同时支持 Webhook 和 WebSocket 模式，且原生实现 `StreamSender` 和 `FileDownloader` 接口。

#### Webhook 模式

```
飞书服务器 ──HTTP POST──▶ /api/v1/im/callback/{channel_id}
                                   │
                           解密 (AES-256-CBC，可选)
                           解析 JSON → IncomingMessage
                                   │
                           通过飞书 Open API 回复
```

- **加密方案：** AES-256-CBC，Key 为 `SHA-256(encrypt_key)`，IV 为密文前 16 字节
- **事件过滤：** 仅处理 `im.message.receive_v1` 事件，忽略其他事件类型
- **消息类型：** `text`（文本）、`file`（文件）、`image`（图片）、`post`（富文本，提取标题 + 结构化内容）
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

**Think 块处理：** 流式输出中的 `<think>...</think>` 块会被转换为飞书 Markdown 引用块格式：

```
> 💭 **思考过程**
> [thinking content line 1]
> [thinking content line 2]
```

**孤立流清理：** 后台协程每 1 分钟扫描超过 5 分钟未关闭的流式卡片，自动调用 `EndStream` 关闭（防止内存泄漏）。

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/feishu/adapter.go` | 事件解析、CardKit 流式实现、Token 缓存、AES 解密、Think 块转换、文件下载 |
| `internal/im/feishu/longconn.go` | WebSocket 长连接（封装飞书 SDK）、事件分发 |

---

### Slack

统一适配器同时支持 Webhook 和 WebSocket (Socket Mode) 模式，且原生实现 `StreamSender` 接口。

#### Webhook 模式 (Events API)

```
Slack 服务器 ──HTTP POST──▶ /api/v1/im/callback/{channel_id}
                                   │
                           签名验证 (HMAC-SHA256)
                           解析 JSON → IncomingMessage
                                   │
                           通过 Slack Web API 回复
```

- **签名验证：** 使用 `signing_secret` 对请求体进行 HMAC-SHA256 签名验证，防止伪造请求。
- **事件过滤：** 仅处理 `message` 和 `app_mention` 事件，忽略机器人自己发送的消息。
- **URL 验证：** 自动处理 Slack 的 `url_verification` challenge 请求。

#### WebSocket 模式 (Socket Mode)

通过 `slack-go/slack/socketmode` 建立长连接，事件推送与 Webhook 等价，无需公网域名，内置自动重连。

```
LongConnClient ══WebSocket══▶ wss://wss-primary.slack.com
       │
  1. 使用 App Token 建立连接
  2. 接收 Events API 消息帧
  3. 确认消息 (Ack)
  4. 通过 Slack Web API 回复
```

#### 流式回复

Slack 的流式输出基于消息更新 (chat.update) 实现：

```
StartStream:
  1. POST /chat.postMessage              → 发送初始消息，获取 ts (timestamp)

SendStreamChunk:
  2. POST /chat.update                   → 根据 ts 更新消息内容 (累积全文)

EndStream:
  3. 无需特殊操作
```

每次 `SendStreamChunk` 发送的是**累积全文**而非增量。

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/slack/adapter.go` | 事件解析、签名验证、流式实现、文件下载 |
| `internal/im/slack/longconn.go` | WebSocket 长连接（封装 slack-go Socket Mode） |

---

### Telegram

统一适配器同时支持 Webhook 和长轮询模式，原生实现 `StreamSender` 和 `FileDownloader` 接口。

#### Webhook 模式

```
Telegram 服务器 ──HTTP POST──▶ /api/v1/im/callback/{channel_id}
                                   │
                           Secret Token 验证（可选）
                           解析 JSON → IncomingMessage
                                   │
                           通过 Telegram Bot API 回复
```

- **签名验证：** 通过 `X-Telegram-Bot-Api-Secret-Token` 请求头与配置的 `secret_token` 进行常量时间比较
- **消息类型：** `text`（文本）、`document`（文件，通过 file_id 下载）、`photo`（图片，自动选择最大尺寸）
- **群消息处理：** 自动去除 `@bot` 提及前缀

#### 长轮询模式 (Long Polling)

Telegram 的 WebSocket 模式实际使用长轮询（Long Polling）而非真正的 WebSocket，通过持续调用 `getUpdates` API 获取新消息。

```
LongConnClient ──HTTP POST──▶ https://api.telegram.org/bot<token>/getUpdates
       │
  1. 发送 getUpdates (timeout=30s，长轮询)
  2. 解析返回的 Update 列表
  3. 更新 offset 以确认已处理
  4. 循环继续
  5. 错误时退避 3 秒重试
```

- **超时设置：** HTTP 客户端 35s，长轮询 timeout 30s
- **消息确认：** 通过 `offset` 参数自动确认已处理的消息

#### 流式回复

Telegram 的流式输出基于消息编辑 (editMessageText) 实现：

```
StartStream:
  1. POST sendMessage               → 发送初始 "正在思考..." 消息，获取 message_id

SendStreamChunk:
  2. POST editMessageText            → 根据 message_id 更新消息内容（累积全文）

EndStream:
  3. POST editMessageText            → 最终更新，启用 Markdown 解析
```

每次 `SendStreamChunk` 发送的是**累积全文**而非增量，最小编辑间隔 500ms（避免触发 Telegram 速率限制）。

**Think 块处理：** 流式输出中的 `<think>...</think>` 块会被转换为 Telegram 引用块格式：

```
> 💭 *思考过程*
> [thinking content line 1]
> [thinking content line 2]
```

**孤立流清理：** 后台协程每 1 分钟扫描超过 5 分钟未关闭的流式消息，自动清理（防止内存泄漏）。

#### 文件下载

Telegram 支持文件和图片消息的下载，流程：
1. 调用 `getFile` API 获取文件路径
2. 通过 `https://api.telegram.org/file/bot<token>/<file_path>` 下载文件

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/telegram/adapter.go` | 事件解析、Secret Token 验证、流式实现（editMessage）、文件下载 |
| `internal/im/telegram/longconn.go` | 长轮询客户端：getUpdates 循环、offset 管理、错误退避 |

---

### 钉钉 (DingTalk)

统一适配器同时支持 Webhook 和 Stream 模式，原生实现 `StreamSender` 接口。支持可选的 AI 卡片流式输出。

#### Webhook 模式

```
钉钉服务器 ──HTTP POST──▶ /api/v1/im/callback/{channel_id}
                                   │
                           HmacSHA256 签名验证
                           解析 JSON → IncomingMessage
                                   │
                           通过 sessionWebhook 或 OpenAPI 回复
```

- **签名验证：** 使用 `client_secret` 对 `timestamp + "\n" + secret` 进行 HmacSHA256 签名，Base64 编码后与请求头 `Sign` 比较，时间戳 1 小时内有效
- **回复方式：** 优先使用回调消息体中的 `sessionWebhook` 回复（适用于群聊场景），失败或不可用时降级到 OpenAPI
  - 群聊：`/v1.0/robot/groupMessages/send`
  - 私聊：`/v1.0/robot/oToMessages/batchSend`
- **消息格式：** Markdown 格式（`sampleMarkdown`）
- **认证：** 通过 `/v1.0/oauth2/accessToken` 获取 Access Token，带缓存（提前 5 分钟刷新）

#### Stream 模式 (WebSocket)

通过钉钉官方 SDK (`github.com/open-dingtalk/dingtalk-stream-sdk-go`) 建立 Stream 连接，事件推送与 Webhook 等价，无需公网域名，内置自动重连。

```
LongConnClient ══Stream══▶ 钉钉 Stream 服务
       │
  1. 使用 ClientID + ClientSecret 建立连接
  2. 注册 ChatBot 回调处理
  3. 接收消息事件
  4. SDK 内置重连和心跳
```

#### 流式回复 (AI 卡片)

钉钉的流式输出基于 **AI 互动卡片** 实现（需配置 `card_template_id`）：

```
StartStream:
  1. POST /v1.0/card/instances/createAndDeliver  → 创建并投递 AI 卡片

SendStreamChunk:
  2. PUT /v1.0/card/streaming                     → 流式更新卡片内容（累积全文）

EndStream:
  3. PUT /v1.0/card/streaming (isFinalize=true)   → 标记流式结束
```

每次 `SendStreamChunk` 发送的是**累积全文**（`isFull: true`），最小更新间隔 500ms。

**无卡片模板时的降级策略：** 未配置 `card_template_id` 时，流式内容在内存中累积，`EndStream` 时一次性通过 `sessionWebhook` 或 OpenAPI 发送完整回复。

**Think 块处理：** 使用与飞书相同的 Markdown 引用块格式（`MarkdownThinkStyle`）：

```
> 💭 **思考过程**
> [thinking content line 1]
> [thinking content line 2]
```

**孤立流清理：** 后台协程每 1 分钟扫描超过 5 分钟未关闭的流，自动清理防止内存泄漏。

#### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/dingtalk/adapter.go` | 事件解析、HmacSHA256 签名验证、AI 卡片流式实现、Access Token 缓存、OpenAPI 调用 |
| `internal/im/dingtalk/longconn.go` | Stream 客户端（封装钉钉 SDK）、ChatBot 消息分发 |

---

## 斜杠指令系统

IM 渠道支持斜杠指令（Slash Commands），用户在聊天中输入 `/指令名` 即可触发，无需经过 QA 管道，且不受限流约束。

### 内置指令

| 指令 | 参数 | 说明 |
|------|------|------|
| `/help` | `[指令名]` | 显示所有可用指令列表；带参数时显示指定指令的详细用法 |
| `/info` | — | 查看当前绑定智能体的名称、角色设定、知识库列表等信息 |
| `/search` | `<关键词>` | 对绑定的知识库执行混合检索（向量 + 关键词），返回最多 5 条原文片段，不经过 AI 总结 |
| `/stop` | — | 取消当前排队中或正在执行的 QA 请求 |
| `/clear` | — | 清空当前对话记忆（软删除 ChannelSession），下次消息开始全新会话 |

### 指令分发流程

```
用户消息 ──▶ HandleMessage
               │
               ├─ 以 "/" 开头？
               │      │
               │      ├─ 已注册指令 → CommandRegistry.Parse → Command.Execute → 回复结果
               │      │                                             │
               │      │                                     ActionClear → 软删除 ChannelSession
               │      │                                     ActionStop  → 取消排队/执行中的 QA
               │      │
               │      └─ LooksLikeCommand() = true 但未注册
               │             → 回复 "未知指令，发送 /help 查看"
               │         LooksLikeCommand() = false (如 "/api/v2/users")
               │             → 当作普通消息，进入 QA 管道
               │
               └─ 普通消息 → 限流检查 → qaQueue → QA 管道
```

> `LooksLikeCommand()` 通过检查首 token 是否含有 `/` 分隔符来区分指令尝试和 URL 路径，避免误拦截。

### 扩展自定义指令

实现 `im.Command` 接口并在 Service 初始化时注册到 `CommandRegistry`：

```go
type Command interface {
    Name() string        // 指令名 (不含 "/")
    Description() string // 一行描述，用于 /help 输出
    Execute(ctx context.Context, cmdCtx *CommandContext, args []string) (*CommandResult, error)
}
```

**设计约定：**
- 依赖（DB、Service）通过构造函数注入，不放在 `CommandContext` 中
- 用户输入错误通过 `CommandResult` 返回友好提示，`error` 仅用于基础设施故障（DB 异常、网络错误等）
- 通过 `CommandResult.Action` 声明副作用意图（如清空会话），由 Service 执行
- 重复注册同名指令会在启动时 panic，确保配置错误尽早暴露

### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/command.go` | Command 接口、CommandAction、CommandContext 定义 |
| `internal/im/command_registry.go` | CommandRegistry：指令注册、解析、分发、LooksLikeCommand |
| `internal/im/cmd_help.go` | `/help` 指令实现 |
| `internal/im/cmd_info.go` | `/info` 指令实现（展示 Agent 信息、知识库列表） |
| `internal/im/cmd_search.go` | `/search` 指令实现（混合检索，最多 5 条，内容截断 200 rune） |
| `internal/im/cmd_stop.go` | `/stop` 指令实现 |
| `internal/im/cmd_clear.go` | `/clear` 指令实现 |

---

## QA 队列与限流

### QA 队列 (qaQueue)

有界工作池队列管理 QA 请求，防止并发过载：

```
消息 ──▶ Enqueue ──▶ [ 等待队列 (≤50) ] ──▶ Worker Pool (5 workers) ──▶ QA 管道
              │                                      │
              ├─ 队列已满 → 拒绝并回复提示               │
              ├─ 用户排队超限 (≤3) → 拒绝               ├─ 等待超时 (>60s) → 丢弃并通知
              └─ /stop → Remove(userKey) 取消           └─ 正常执行 QA
```

**设计要点：**

- **有界队列**：最大容量 50，防止内存无限增长
- **Per-User 背压**：单用户最多同时排队 3 个请求，避免单用户刷屏占满队列
- **排队等待提示**：入队成功且队列非空时，回复 "前面还有 N 条消息在处理，请稍候"
- **排队超时**：请求在队列中等待超过 60 秒自动丢弃，回复超时提示
- **可取消**：`/stop` 指令通过 `qaQueue.Remove(userKey)` 取消排队请求，通过 `inflight` map 中的 `context.CancelFunc` 取消执行中请求
- **指标监控**：每 30 秒输出队列深度、活跃 Worker 数、入队/处理/拒绝/超时计数（仅在有活动时输出）

### 滑动窗口限流 (rateLimiter)

在消息进入 QA 队列之前，按 `channelID:userID:chatID` 维度进行滑动窗口限流：

| 参数 | 值 | 说明 |
|------|------|------|
| 窗口大小 | 60s | 滑动时间窗口 |
| 最大请求数 | 10 次/窗口 | 每个用户每分钟最多 10 条消息进入 QA |
| 清理周期 | 1 min | 自动清理过期条目，防止内存泄漏 |

超出限流时回复提示消息，不计入队列。斜杠指令不受限流约束。

### 源码文件

| 文件 | 职责 |
|------|------|
| `internal/im/qaqueue.go` | qaQueue：有界队列、Worker Pool、QueueMetrics、指标上报 |
| `internal/im/ratelimit.go` | slidingWindowLimiter：per-key 滑动窗口限流、并发安全清理 |

---

## 流式输出机制

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

### 内容处理

- **Think 块过滤/转换**：`<think>...</think>` 块在飞书和钉钉中转换为 Markdown 引用块展示，在 Telegram 中转换为引用块（斜体标题），在其他平台中过滤
- **工具事件展示**：Agent 工具调用实时展示调用状态
  - 调用中：`⏳ [工具名]`（包裹在 think 块内）
  - 调用成功：`✅ [工具名] · [摘要]`
  - 调用失败：`⚠️ [工具名] 失败`
  - 内部工具（thinking、todo_write 等）不展示给用户
- **空内容回退**：流式过程中无可见内容产生时，回退到完整回复模式 (`fallbackNonStream`)
- **完整持久化**：完整内容（含 thinking）持久化到数据库，确保历史完整

### 平台流式特殊处理

- **"正在思考..." 占位**：飞书和 Telegram 在流式初始化后立即显示占位文本，提升用户感知
- **孤立流清理**：飞书、Telegram、钉钉的后台协程每 `streamReaperInterval`（1 分钟）扫描超过 `streamOrphanTTL`（5 分钟）未关闭的流，自动关闭防止内存泄漏
- **Think 块转换**：飞书和钉钉将 `<think>` 标签转换为 Markdown 引用块（`> 💭 **思考过程**`），Telegram 使用斜体标题（`> 💭 *思考过程*`）

---

## 文件消息处理

当用户在 IM 中发送文件或图片消息时，如果渠道配置了 `knowledge_base_id`，Service 会自动将文件保存到对应知识库：

```
用户发送文件/图片消息
        │
        ▼
  消息类型 = file/image？
  渠道配置了 knowledge_base_id？
  Adapter 实现了 FileDownloader？
        │ 全部满足
        ▼
  1. adapter.DownloadFile(msg) → io.ReadCloser + fileName
  2. 通知用户 "正在处理文件..."
  3. knowledgeService.Save(file, knowledgeBaseID)
  4. 通知用户 "文件已保存到知识库"
```

**各平台文件下载方式：**

| 平台 | 方式 |
|------|------|
| 飞书 | GetMessageResource API（通过 FileKey） |
| 企业微信 Webhook | PicUrl 直接下载 或 MediaId 临时素材 API |
| 企业微信 WebSocket | 加密附件 URL + per-message AES 密钥解密 |
| Telegram | getFile API 获取文件路径 + HTTPS 下载（支持文档和图片） |

---

## 关键参数与阈值

| 参数 | 值 | 说明 |
|------|------|------|
| `qaTimeout` | 120s | QA 管道最大执行时间 |
| `dedupTTL` | 5 min | 消息去重 ID 保留时长 |
| `dedupCleanupInterval` | 1 min | 去重清理周期 |
| `maxContentLength` | 4096 | 消息最大长度 (rune)，超出截断 |
| `streamFlushInterval` | 300ms | 流式内容批量刷新间隔 |
| `defaultMaxQueueSize` | 50 | QA 队列最大容量 |
| `defaultMaxPerUser` | 3 | 单用户最大排队请求数 |
| `defaultWorkers` | 5 | QA 并发 Worker 数 |
| `queueTimeout` | 60s | 请求在队列中的最大等待时间 |
| `rateLimitWindow` | 60s | 限流滑动窗口大小 |
| `rateLimitMaxRequests` | 10 | 每用户每窗口最大请求数 |
| `metricsLogInterval` | 30s | 队列指标日志上报周期 |
| `streamOrphanTTL` | 5 min | 飞书/Telegram/钉钉 孤立流超时时间 |
| `streamReaperInterval` | 1 min | 飞书/Telegram/钉钉 孤立流清理扫描周期 |
| Telegram 编辑间隔 | 500ms | editMessageText 最小调用间隔（避免速率限制）|
| Telegram 长轮询超时 | 30s | getUpdates timeout 参数 |
| Telegram 错误退避 | 3s | getUpdates 失败后等待时间 |
| DingTalk 卡片更新间隔 | 500ms | AI 卡片流式更新最小间隔 |
| DingTalk 签名有效期 | 1h | Webhook 回调签名时间戳有效窗口 |
| WeCom WS 心跳 | 30s | WebSocket 保活频率 |
| WeCom WS 读超时 | 90s | 3 × 心跳间隔，允许一次心跳丢失 |
| WeCom WS 重连退避 | 1s → 30s | 指数退避，上限 30 秒 |
| Token 缓存安全余量 | 5 min | Token 过期前提前刷新 |

---

## 错误处理

| 场景 | 处理策略 |
|------|---------|
| 流式初始化失败 | 自动降级到全量模式 (`fallbackNonStream`) |
| QA 管道异常 | 回复 "抱歉，处理您的问题时出现了异常，请稍后再试。" |
| QA 超时 (>120s) | 标记消息完成，回复超时提示 |
| 空回答 | 回复 "抱歉，我暂时无法回答这个问题。" |
| 空流式内容 | 无可见内容时回退到完整回复 |
| WebSocket 断连 | 指数退避自动重连 |
| 平台重试 | MessageID 去重，5 分钟内自动跳过 |
| 渠道启动失败 | 日志记录错误，不影响其他渠道 |
| QA 队列已满 | 拒绝请求并回复 "当前排队人数较多，请稍后再试。" |
| 用户排队超限 | 拒绝请求并回复提示（单用户 ≤3） |
| 排队等待超时 | 超过 60s 自动丢弃，回复 "您的消息等待超时，请重新发送。" |
| 消息限流 | 滑动窗口内超过 10 次，回复限流提示 |
| 飞书孤立流 | 每分钟扫描，超过 5 分钟未关闭的自动结束 |
| Telegram/钉钉孤立流 | 同飞书，每分钟扫描并自动清理 |
| 企业微信群聊回复失败 | appchat API 失败时降级到用户私聊 |
| 钉钉 AI 卡片创建失败 | 降级到 sessionWebhook 或 OpenAPI 回复 |
| 钉钉 sessionWebhook 不可用 | 降级到 OpenAPI（群聊/私聊分别调用不同端点） |

---

## 扩展新平台

接入新的 IM 平台只需 3 步：

### 1. 实现 `im.Adapter` 接口

在 `internal/im/<platform>/` 下创建适配器：

```go
package myplatform

type Adapter struct { /* 平台配置 */ }

func (a *Adapter) Platform() im.Platform     { return "myplatform" }
func (a *Adapter) VerifyCallback(c *gin.Context) error { /* 签名验证 */ }
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) { /* 解析消息 */ }
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error { /* 发送回复 */ }
func (a *Adapter) HandleURLVerification(c *gin.Context) bool { /* URL 验证 */ }
```

可选接口：
- 实现 `im.StreamSender` 以支持流式输出
- 实现 `im.FileDownloader` 以支持文件消息自动保存到知识库

> 可参考已有的 Telegram（纯 HTTP API）和 DingTalk（SDK + AI 卡片）适配器作为实现参考。

### 2. 注册适配器工厂

在 `internal/container/container.go` 的 `registerIMAdapterFactories` 中注册工厂函数：

```go
imService.RegisterAdapterFactory("myplatform", func(ctx context.Context, channel *im.IMChannel, msgHandler func(*im.IncomingMessage)) (im.Adapter, im.CancelFunc, error) {
    creds := parseCredentials(channel.Credentials)
    appKey := getString(creds, "app_key")
    appSecret := getString(creds, "app_secret")

    adapter := myplatform.NewAdapter(appKey, appSecret)

    // WebSocket 模式需要启动长连接
    if channel.Mode == "websocket" {
        cancelCtx, cancel := context.WithCancel(ctx)
        go adapter.StartLongConn(cancelCtx, msgHandler)
        return adapter, func() { cancel() }, nil
    }

    return adapter, func() {}, nil
})
```

### 3. 前端添加平台选项

在 `IMChannelPanel.vue` 中：
- 添加平台 radio 选项
- 添加该平台的凭证表单字段

在 i18n 文件中添加平台名称翻译。

Service 层 (`im.Service`) 不需要任何修改 — 渠道管理、指令分发、消息编排、会话管理、QA 调度、限流、流式控制全部由 Service 统一处理。

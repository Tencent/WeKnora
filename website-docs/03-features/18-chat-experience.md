# 会话与对话体验

前面的章节讲的是「知识怎么进来、怎么被检索」，这一章讲**对话框本身**：一次问答过程中用户看到什么、能做什么。这些能力分散在会话、消息、附件、建议问题几组接口上，这里集中说明。

## 1. 一轮问答里用户看到的东西

| 界面元素 | 说明 |
| --- | --- |
| 流水线进度条 | 回答生成前展示当前阶段：附件解析、图片理解、检索文档、联网、工具调用、思考、生成回答 |
| 思考过程 | 模型的推理内容内联展示在 Agent 时间线里，可折叠 |
| 引用角标 | 回答正文中的来源标记，点击定位到原文分块 |
| 引用面板（references drawer） | 侧栏列出本轮所有检索来源，含 Wiki 工具的返回结果 |
| 追问建议 | 回答结束后给出的下一步问题，见 [Agent 引擎](07-agent.md)的「建议问题」 |

<Screenshot
  src="/screenshots/chat-references-drawer.png"
  caption="对话页：回答、引用角标与右侧引用面板"
  hint="展示一轮带引用的回答、展开的引用面板（含来源标题与片段），以及顶部会话操作栏。" />

### 进度条的两种等待态

所有可见阶段都完成、但模型还没吐字时会有一段静默期，进度条据此区分两种提示：确实跑过检索的显示「正在生成回答」，纯附件问答这类没有检索步骤的显示中性的「准备中」。超过 60 秒仍无回答转为停滞态——SSE 断连时后端不会再发完成事件，没有这个上限进度条会一直宣称「马上就好」。实现见 [Web 前端](../05-clients/01-frontend.md)。

### 引用开不开，与引用面板无关

Agent 配置里的 `citation_enabled` 只控制**回答正文里的角标**。关掉之后正文变干净，但检索来源照常送进引用面板——也就是说「不显示引用」不等于「不给出处」。该字段为 `nil` 时按开启处理，保证这个选项引入之前保存的 Agent 行为不变。

### 导出对话

会话操作栏可以把整段对话导出为 Markdown（`frontend/src/utils/sessionMarkdown.ts` 的 `buildSessionMarkdown()`），内容包含会话标题、ID、导出时间与逐轮问答，适合贴进工单或周报。导出走前端，不产生额外接口调用。

## 2. 会话内临时附件

对话里可以直接丢文件进去问，不必先建知识库——这类文件叫**临时文档**（`temporary_documents` 表，migration `000070`），只属于当前会话。

接口（均在 `/api/v1/sessions` 下）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/:session_id/attachments` | 上传附件 |
| GET | `/:id/attachments` | 列出本会话附件 |
| GET | `/:id/attachments/:attachment_id` | 附件详情 |
| GET | `/:id/attachments/:attachment_id/preview` | 预览 |
| DELETE | `/:id/attachments/:attachment_id` | 删除 |

行为要点：

- 状态机：`uploaded` → `processing` → `ready`，解析是异步的。发问时如果附件还没解析完，会等待到 `WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC`（默认 60 秒，扫描件建议调大）；
- 解析产物保留 `WEKNORA_CHAT_ATTACHMENT_TTL_HOURS`（默认 24 小时）后清理，附件不会长期占用存储；
- 扫描件/图片型文档走 VLM OCR，并发与页数上限由 `WEKNORA_CHAT_ATTACHMENT_OCR_CONCURRENCY`（默认 8）与 `WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES`（默认 8）控制；
- Agent 侧还有三个相关配置：`supported_file_types`（限定可传类型）、`attachment_image_understanding`（是否理解图片）、`chat_parser_engine_rules`（附件走哪个解析引擎），见 [Agent 引擎](07-agent.md)；
- 临时附件与知识库文档是两套东西：它不进向量索引、不出现在知识库列表里，会话结束即失效。需要长期检索的资料应该正式入库。

## 3. 渠道会话的可见性

除了网页对话，IM 机器人、网页挂件访客、API Key 调用也都会产生会话。这些「渠道会话」在控制台里**默认不可见**，因为它们按 Key、访客、IM 身份各自隔离。

规则在 `internal/application/service/session.go`：

- 会话列表的 `source` 过滤器为空或 `web` 时，只返回调用者自己的会话；
- 过滤 `api` / `im` / `embed` 属于**空间级视角**，要求 Admin+，否则返回 403（`listing channel sessions requires tenant admin or owner role`）。通过校验后会去掉按用户的收窄，管理员因此能观察到这些原本互相隔离的会话；
- 侧栏里的 IM / 嵌入 / API 分组也是管理员专属，且会先探测数量，有会话才显示，避免给普通用户留一个永远空着的入口；
- 即使是管理员，打开渠道会话也只是**只读观察**；API Key 产生的会话在写接口上始终按归属收窄。

这个设计的用意是：管理员需要排查「机器人昨天怎么答的」，但不该让普通成员翻到别人的客服对话。

## 4. 跨会话历史搜索

聊天记录可以被索引并跨会话检索：

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| POST | `/api/v1/messages/search` | Viewer+；API Key 需 `message_history` 能力或 full-access |
| GET | `/api/v1/messages/chat-history-stats` | 同上 |
| GET | `/api/v1/messages/:session_id/load` | Viewer+；API Key 需 `chat` 能力（只能读自己会话） |

`message_history` 是一个独立能力，用意是让做数据分析的集成能搜历史元数据，而不必给它一把 full-access Key。开关与保留策略在「设置 → 聊天历史」（`chathistory` 分区，需 Admin）。

## 5. 相关章节

- 建议问题（开场问题与追问）：[Agent 引擎](07-agent.md)
- 回答里的图片与文件怎么送到客户端：[API 总览](../04-api/01-api-overview.md)的「文件引用形式」
- IM 与网页挂件各自的会话模型：[IM 集成](12-im-integration.md)、[网页嵌入](13-embed-channel.md)
- 会话与消息的完整接口：[API 参考：会话与聊天](../04-api/02-api-chat.md)

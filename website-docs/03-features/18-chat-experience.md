# 会话与对话体验

在会话中，可以围绕知识库或上传的附件提问，查看回答来源，并预览智能体生成的文件。问题大纲和历史搜索用于回顾已有对话，Markdown 导出用于保存完整问答记录。

## 查看回答与来源

提问后，进度条显示附件解析、检索、工具调用和回答生成等阶段。智能推理模式还会展示可折叠的思考过程、命令输出和文件写入进度。

回答正文中的引用角标用于定位原文分块，右侧引用面板列出本轮使用的检索来源，包括 Wiki 工具返回的内容。关闭智能体的引用角标设置后，仍可通过引用面板查看来源。回答结束时，系统可提供追问建议，详见 [Agent 引擎](07-agent.md)。

<Screenshot
  src="/screenshots/chat-references-drawer.png"
  caption="对话页：回答、引用角标与右侧引用面板"
  hint="展示一轮带引用的回答、展开的引用面板（含来源标题与片段），以及顶部会话操作栏。" />

检索完成后，等待模型返回内容的阶段显示“正在生成回答”；没有执行检索的问答显示“准备中”。等待超过 60 秒时，界面会更新为长时间等待提示。这一提示反映当前等待状态，回答和工具执行结果以实际返回内容为准。

## 使用临时附件提问

在对话中上传文件，即可围绕文件内容提问。附件仅供当前会话使用，不进入知识库列表或向量索引。需要长期检索、共享的资料应上传到知识库。

上传后，系统异步解析文件，并显示上传、处理中和就绪状态。发问时尚未完成解析的附件会继续等待，默认等待上限为 60 秒。扫描件和图片型文档可通过视觉模型识别文字。

临时附件从上传时起计算保留期限，默认 24 小时，到期后由后台任务清理。管理员可调整保留期限、解析等待时间及 OCR 参数，具体配置见文末的“配置与接口参考”。

智能体可配置允许上传的文件类型、图片理解开关，以及按文件类型选择的解析引擎。相关说明见 [Agent 引擎](07-agent.md)。

## 预览和下载生成文件

绑定沙箱的智能体可以处理附件并生成文件。回答完成后，本轮生成的文件显示在回答下方，也可在会话文件面板中集中查看。支持的文件类型可以直接预览，其余文件可下载后打开。

需要让智能体生成可下载文件时，产物应保存到 `/workspace/output`，系统会在回答结束时收集并持久化。上传的附件暂存于 `/workspace/input`，脚本和中间文件可放在 `/workspace` 的其他目录。输入附件和输出文件分别管理，删除输入附件不会自动删除已收集的输出文件。

文件读取、脚本执行和目录约定见[技能与沙箱](22-skills-sandbox.md)；列表与下载接口见[会话与聊天 API](../04-api/02-api-chat.md)。

## 回顾和导出对话

长对话中的问题大纲可直接定位到某次提问，时间分隔用于区分不同日期的消息。侧栏标记仍在执行的会话，并显示 API 会话的来源和归属。

会话操作栏支持导出 Markdown，内容包括会话标题、ID、导出时间和逐轮问答。导出在浏览器中完成，可用于归档或分享对话记录。

启用聊天历史索引后，可以跨会话搜索已有消息。空间管理员在“设置 → 聊天历史”配置索引开关与保留策略。

## 查看渠道会话

IM、网页嵌入和 API 调用产生的会话，分别按 IM 身份、访客或 API Key 隔离。普通会话列表默认只展示当前调用者自己的会话。

空间 Admin 或 Owner 可以通过 IM、嵌入和 API 分组查看对应渠道会话；分组在存在会话时显示。管理员通过这一入口进行只读查看，用于检查渠道中的问答记录。API Key 会话的写操作继续受原会话归属限制。

## 使用跨会话记忆

空间启用长期记忆后，可以在个人记忆管理中维护表达偏好、确认待确认条目，并在后续会话中使用。记忆按空间和调用者身份隔离。

关闭个人记忆会暂停使用，删除或清空则用于移除数据。自动提取、主题管理和立即整理的说明见[长期记忆](23-memory.md)。

## 配置与接口参考

以下参数供管理员和集成开发者查询。日常对话操作使用前述界面入口即可。

### 附件配置

| 配置项 | 默认值 | 作用 |
| --- | --- | --- |
| `WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC` | 60 秒 | 发问时等待附件完成解析的上限 |
| `WEKNORA_CHAT_ATTACHMENT_TTL_HOURS` | 24 小时 | 从上传时起计算的附件保留期限 |
| `WEKNORA_CHAT_ATTACHMENT_OCR_CONCURRENCY` | 8 | OCR 并发数 |
| `WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES` | 8 | OCR 页数上限 |

智能体配置中的 `supported_file_types` 限定允许上传的类型，`attachment_image_understanding` 控制图片理解，`chat_parser_engine_rules` 指定附件解析引擎。

附件 API 使用 `/api/v1/sessions` 前缀：

| 操作 | 方法与路径 |
| --- | --- |
| 上传 | `POST /:session_id/attachments` |
| 列表 | `GET /:id/attachments` |
| 详情 | `GET /:id/attachments/:attachment_id` |
| 预览 | `GET /:id/attachments/:attachment_id/preview` |
| 删除 | `DELETE /:id/attachments/:attachment_id` |

完整参数和权限见[会话与聊天 API](../04-api/02-api-chat.md)。

### 引用和历史查询

智能体的 `citation_enabled` 控制回答正文中的引用角标，省略时默认开启；引用面板不受该字段影响。

历史搜索使用 `POST /api/v1/messages/search`，索引统计使用 `GET /api/v1/messages/chat-history-stats`，两者要求 Viewer+，API Key 需 `message_history` 能力或 full-access。读取会话历史使用 `GET /api/v1/messages/:session_id/load`，要求 Viewer+；API Key 需 `chat` 能力，并受会话归属校验。

会话列表的 `source` 为空或 `web` 时查询自己的会话；显式查询 `api`、`im` 或 `embed` 的空间视图要求 Admin+，权限不足返回 403。

## 实现参考

- `frontend/src/views/chat/components/`：回答、进度与工具过程展示。
- `frontend/src/utils/sessionMarkdown.ts`：Markdown 导出。
- `internal/application/service/session.go`：会话列表与渠道访问规则。
- `internal/application/service/temporary_document.go`：临时附件解析、到期与清理。
- 临时附件存储在 `temporary_documents` 表，表结构见[数据库与迁移](../06-development/02-database-schema.md)。

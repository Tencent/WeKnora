# Agent 引擎

提示词分段、模板引用和自定义正文的维护方式见[对话提示词拼装与可编辑范围](../06-development/05-agent-prompts.md)；浏览器配对与部署见[本机浏览器](../05-clients/09-local-browser.md)。

智能体可结合知识库检索、联网搜索和外部工具处理多步骤任务，例如比较多份合同的条款。智能推理模式按问题选择工具并执行多轮调用，再根据获得的结果生成回答。

对话框顶部可选择快速问答或智能推理：

| 模式 | 适用任务 | 执行特点 |
| --- | --- | --- |
| 快速问答（quick-answer） | 基于文档的事实查询 | 检索后生成回答，通常调用次数较少 |
| 智能推理（smart-reasoning） | 跨文档分析、联网查询或工具操作 | 可能执行多轮调用，耗时与用量取决于任务 |

在「智能体」页可以创建自定义智能体，选择模式和模型，限定知识库范围，并配置提示词、联网搜索、MCP 工具及技能。保存后可用于网页对话，也可绑定到 IM 或嵌入渠道。

<Screenshot
  src="/screenshots/agent-editor.png"
  caption="自定义 Agent 配置：模式、模型、知识范围与工具"
  hint="展示 Agent 编辑弹窗，含模式选择、模型选择、知识库范围、联网搜索开关与 MCP 工具勾选。" />

<Screenshot
  src="/screenshots/agent-chat.png"
  caption="Agent 对话：推理过程与工具调用时间线"
  hint="展示一轮 Agent 回答，包含展开的思考步骤、工具调用卡片与最终答案的引用。" />

配置决定智能体可访问的资料与工具，实际调用仍受当前用户或渠道的权限约束。

## 创建与使用智能体

1. 在「智能体」页新建智能体，选择快速问答或智能推理模式。
2. 选择模型、知识库范围和提示词。类型预设会预填配置，保存前仍可调整。
3. 根据任务启用联网搜索或选择 MCP 工具；运行技能脚本时还需绑定已安装技能的沙箱。
4. 保存后在对话页选择该智能体，完成一次提问，检查回答来源与工具结果。

初次使用可直接选择内置智能体。快速问答适用于文档查询；数据分析智能体面向 CSV 和 Excel；Wiki 智能体用于浏览和维护 Wiki 内容。

智能体配置的思考强度（`reasoning_effort`）是默认值，对话时可在输入框临时调整；智能推理回答生成期间还可以继续补充要求。见[会话与对话体验](18-chat-experience.md)。

## 设置资料和工具范围

智能体可以使用全部、指定或禁用的知识库及技能范围。对话中的提及用于选择本轮资料或提示优先技能，不能绕过已有授权；输入框的 @技能、@MCP 菜单只列出本轮实际运行的智能体可用的资源，使用共享智能体时列出来源空间的技能和该智能体显式选择的 MCP 服务。联网搜索同时受智能体配置和本轮请求开关约束。

通过组织共享智能体后，接收方在授权范围内使用来源空间的模型与资料。共享智能体为只读，接收方不能修改其配置。接收方使用时：

- 始终使用智能体配置的模型，请求里的 `summary_model_id` 会被忽略；
- 未设置 MCP 选择模式的智能体不使用 MCP 服务；
- 对话记录写入接收方自己空间的对话记录知识库，不会写入来源空间；
- 接收方能看到智能体的能力与资源范围（模型、知识库、MCP、联网搜索），看不到提示词和创建人。

启用了技能的智能体共享后，技能在来源空间的沙箱中运行，并带上管理员为技能配置的环境变量，成员可以让智能体读出这些值。共享规则见[空间与权限](01-tenant-auth.md)。

## 处理工具审批与授权

需要人工审批的 MCP 工具在执行前显示审批卡片，用户可批准、拒绝或修改参数。默认等待上限为 10 分钟；拒绝、超时或取消会作为工具结果返回，智能体可据此继续处理。此审批机制只用于 MCP 工具。

MCP 服务需要 OAuth 授权时，可在当前对话中完成授权，成功后系统会重试工具调用。

## 使用技能、附件和记忆

绑定沙箱后，智能推理可读取附件、运行脚本并生成文件。可下载的产物应写入 `/workspace/output`，回答完成后可在会话中预览和下载。每轮回答结束后，系统为 `/workspace` 记录一个 git 检查点，分叉和回滚会话时据此恢复对应轮次的工作区；桌面版直接使用本机目录的会话不记录检查点，分叉和回滚只作用于对话。安装与变量配置见[技能目录与沙箱](22-skills-sandbox.md)，附件操作见[会话与对话体验](18-chat-experience.md)。

长期记忆按空间和调用者隔离，智能体可单独关闭记忆读写；完整说明见[跨会话长期记忆](23-memory.md)。

## 配置参考

### 自定义 Agent {#_7-自定义-agent}

#### 模式与类型预设 {#_7-1-模式与类型预设}

`CustomAgent`（`internal/types/custom_agent.go`）有两个运行模式（`Config.AgentMode`）：

- `quick-answer`：经典 RAG 管道（检索→拼上下文→单次生成），不进 Agent 引擎；
- `smart-reasoning`：ReAct Agent 模式，`IsAgentMode()` 返回 true，并强制 `MultiTurnEnabled = true`。

smart-reasoning 下还可选**类型预设**（`Config.AgentType`，定义在 `config/agent_type_presets.yaml`，由 `internal/types/agent_type_preset.go` 加载）。预设只在编辑器里**预填表单**，用户可任意覆盖：

| 预设 ID | 系统提示词模板 | 温度 | 最大迭代 | 预填工具 | KB 过滤 |
| --- | --- | --- | --- | --- | --- |
| `rag-qa` | `progressive_rag_agent` | 0.7 | 30 | search_knowledge、read_document、list_documents | 由工具派生：any_of vector/keyword |
| `wiki-qa` | `wiki_researcher` | 0.7 | 30 | wiki_search、wiki_read_page、read_document、wiki_flag_issue | 由工具派生：any_of wiki |
| `hybrid-rag-wiki` | `hybrid_rag_wiki_agent` | 0.7 | 40 | wiki_search、wiki_read_page、search_knowledge、read_document、list_documents、wiki_flag_issue | any_of vector/keyword/wiki |
| `data-analysis` | `data_analyst` | 0.3 | 30 | data_schema、data_analysis；关闭 web 搜索；限定文件类型 csv/xlsx | 显式 `none_of: [faq]` |
| `custom` | 无 | — | — | 不预填 | 不限制 |

`thinking` 和 `todo_write` 默认不包含在预设工具中，使用时需手动选择；启用会增加 token 开销。

#### 可配置项（CustomAgentConfig） {#_7-2-可配置项-customagentconfig}

`internal/types/custom_agent.go` 中 `CustomAgentConfig` 的主要字段（handler `CreateAgent`/`UpdateAgent` 直接接收该结构）：

| 分类 | 字段 | 说明 / 默认（EnsureDefaults） |
| --- | --- | --- |
| 基础 | `agent_mode` | `quick-answer` / `smart-reasoning` |
| 基础 | `agent_type` | smart-reasoning 下的预设类别，空/未知视为 custom |
| 基础 | `system_prompt` / `system_prompt_id` | 直接正文优先；自定义 Agent 的模板引用由 `ResolveCustomAgentPrompts` 在请求时解析 |
| 基础 | `context_template` / `context_template_id` | 普通模式下检索片段的拼装模板 |
| 模型 | `model_id`、`rerank_model_id`、`temperature`、`max_completion_tokens`、`thinking`、`reasoning_effort`、`citation_enabled` | temperature<0 → 0.7；max_completion_tokens=0 使用运行时默认：quick-answer 2048、smart-reasoning 4096、绑定沙箱的 smart-reasoning 24576；绑定沙箱时显式值低于 8192 按 8192 执行；`reasoning_effort` 取 `off`/`auto`/`minimal`/`low`/`medium`/`high`/`xhigh`/`max`，设置后优先于 `thinking`（`thinking: true` 等同 `auto`），模型不支持的档位在调用时自动就近调整；两者都未设时不开启思考；单次对话可用请求字段 `reasoning_effort` 临时覆盖；citation 未设时视为 true |
| Agent | `max_iterations` | 默认 10，负数表示不限轮数（服务层上限 100） |
| Agent | `llm_call_timeout` | 单次模型流式调用允许连续无输出的秒数，0 用默认 120s；总时长由模型传输层控制 |
| Agent | `allowed_tools` | 工具白名单；空回退 DefaultAllowedTools |
| MCP | `mcp_selection_mode`（all/selected/none）、`mcp_services`、`mcp_auth_wait_timeout` | OAuth 等待秒数 <=0 用 Gate 默认 |
| 技能 | `skills_selection_mode`（all/selected/none）、`selected_skills`、`sandbox_config_id` | 选择空间沙箱及其已安装技能，见[技能目录与沙箱](22-skills-sandbox.md) |
| 记忆 | `memory_enabled` | nil 继承空间，false 禁用本智能体的记忆读写 |
| 知识库 | `kb_selection_mode`（all/selected/none）、`knowledge_bases`、`retrieve_kb_only_when_mentioned`、`retain_retrieval_history` | retain=true 时历史 KB 检索结果不脱敏 |
| 多模态 | `image_upload_enabled`、`vlm_model_id`、`audio_upload_enabled`、`asr_model_id`、`image_storage_provider` | VLM 也用于 MCP 工具返回图片的描述 |
| 文件 | `supported_file_types`、`chat_parser_engine_rules`、`attachment_image_understanding`、`attachment_ocr_max_pages`、`attachment_parse_wait_timeout_sec` | 数据分析型 Agent 常限定 csv/xlsx |
| FAQ | `faq_priority_enabled`、`faq_direct_answer_threshold`、`faq_score_boost` | — |
| Web | `web_search_enabled`、`web_search_max_results`、`web_search_provider_id`、`web_fetch_enabled`、`web_fetch_top_n` | max_results 默认 5；`web_fetch_*` 只作用于 quick-answer 管道，智能推理由模型自行调用 `web_fetch` |
| 多轮 | `multi_turn_enabled`、`history_turns` | history_turns 默认 5，只约束普通模式（KnowledgeQA）；smart-reasoning 强制 multi_turn，历史按上下文窗口加载，不读 history_turns |
| 检索 | `embedding_top_k`（10）、`keyword_threshold`（0.3）、`vector_threshold`（0.5）、`rerank_top_k`（5）、`rerank_threshold` | 括号内为默认值 |
| 高级 | `enable_query_expansion`、`enable_rewrite`、`rewrite_prompt_*`、`query_understand_model_id`、`fallback_strategy`（默认 model）、`fallback_response`、`fallback_prompt`、`intent_prompts`、`data_analysis_enabled` | 主要作用于 quick-answer 管道 |
| 建议 | `question_suggestions`（starters / follow_ups） | starters 默认 hybrid 模式 6 条；follow_ups 默认关闭、3 条 |

Handler 层（`internal/handler/custom_agent.go`）提供 `CreateAgent`、`GetAgent`、`ListAgents`、`UpdateAgent`、`DeleteAgent`、`CopyAgent`、`GetPlaceholders`（返回 `types.PlaceholdersByField(PromptFieldAgentSystemPrompt)` 的占位符清单）、`GetAgentTypePresets`（带 i18n 的预设列表）、`GetSuggestedQuestions`。创建/更新时经 `authorizeAgentKnowledgeScope` 校验受限 API Key 的 KB 范围：`kb_selection_mode: all` 对 KB 受限 key 直接 403，`selected` 逐一鉴权。

运行时映射：`buildAgentConfig`（`session_agent_qa.go`）把 `CustomAgentConfig` 转换为引擎的 `types.AgentConfig`（`internal/types/agent.go`），并叠加：web 搜索需 Agent 与请求同时开启（`customAgent.Config.WebSearchEnabled && req.WebSearchEnabled`）、web provider 回退租户默认、`SearchTargets` 由 KB/@文档/@标签 scope 统一构建、`MaxContextTokens` 兜底 200000、`@Skill` 与 `@MCP` 的每轮优先提示（不移除其他已配置资源）（共享 Agent 的 @MCP 只能落在 Agent 预设集合内）。另外只有当 `search_knowledge` 实际可用时才要求配置 rerank 模型（`agentRequiresRerankModel`，旧名 `knowledge_search` / `grep_chunks` 经 `SuccessorToolName` 归一后同样计入）。

#### 分享机制（agent_share） {#_7-3-分享机制-agent-share}

`internal/application/service/agent_share.go`：Agent 可分享给**组织（Organization）**：

- 仅 Agent 属主租户可分享（`ErrNotAgentOwner`）；分享者所在租户须为组织 Editor+ 成员；
- 分享前校验 Agent 配置完整：必须有 `model_id`；若 `search_knowledge` 在其工具集内（或工具集为空回退默认集）且 KB scope 未禁用，还必须有 `rerank_model_id`，否则 `ErrAgentNotConfigured`；
- **权限强制为只读**：`permission = types.OrgRoleViewer`（跨租户编辑不在 v1 范围）；重复分享则幂等更新；
- 接收方租户可通过 `TenantDisabledSharedAgentRepository` 把某个共享 Agent 在本租户禁用；
- 使用共享 Agent 对话时（`session_agent_qa.go`），检索与模型 scope 切到 **Agent 属主租户**（`resolveRetrievalTenantID`），因此共享方的 KB 对使用方可用，而使用方自己的 MCP @提及会被限制在 Agent 预设内。

### 内置 Agent（config/builtin_agents.yaml） {#_8-内置-agent-config-builtin-agents-yaml}

内置 Agent 由 `config/builtin_agents.yaml` 定义，启动时 `types.LoadBuiltinAgentsConfig` 载入并重建 `BuiltinAgentRegistry`（`internal/types/builtin_agent_config.go`），支持 default/zh-CN/zh-TW/ja-JP/ko-KR 多语言名称与描述；`system_prompt_id`/`context_template_id` 在启动时经 `ResolveBuiltinAgentPromptRefs` 解析为具体模板内容。

| ID | 名称（zh-CN） | agent_mode / agent_type | 关键配置 |
| --- | --- | --- | --- |
| `builtin-quick-answer` | 快速问答 | `quick-answer` | 模板 `default_kb` + `default_context`；temperature 0.7；FAQ 优先（直接回答阈值 0.9、加权 1.2）；query expansion + rewrite；web 搜索开、5 条；不进 Agent 引擎 |
| `builtin-smart-reasoning` | 智能推理 | `smart-reasoning` / `rag-qa` | `max_iterations: 50`；工具：search_knowledge、read_document、list_documents、query_knowledge_graph；web 搜索开；多轮（历史按上下文窗口加载） |
| `builtin-data-analyst` | 数据分析师 | `smart-reasoning` / `data-analysis` | 模板 `data_analyst`；temperature 0.3；`max_iterations: 30`；工具仅 data_schema + data_analysis；限定 csv/xlsx；关闭 web 搜索；多轮（历史按上下文窗口加载） |
| `builtin-wiki-researcher` | 维基问答 | `smart-reasoning` / `wiki-qa` | 模板 `wiki_researcher`；`max_iterations: 30`；工具：wiki_search、wiki_read_page、read_document、wiki_flag_issue（只读 + 报障）；关闭 web 搜索 |
| `builtin-wiki-fixer` | 维基修订 | `smart-reasoning` / `custom` | 模板 `wiki_fixer`；`retain_retrieval_history: true`（修订需要跨轮记住页面内容）；工具共 9 个：wiki_search、wiki_read_page、read_document、wiki_write_page、wiki_replace_text、wiki_rename_page、wiki_delete_page、wiki_read_issue、wiki_update_issue（不含 wiki_flag_issue）；`kb_selection_mode: selected` |
| `builtin-skill-installer` | 技能安装器 | `smart-reasoning` / `custom` | 模板 `skill_installer`；temperature 0.2；`max_completion_tokens: 24576`；`max_iterations: 30`；工具：shell_exec、write_skill_file、edit_skill_file；`kb_selection_mode: none`；由沙箱配置的技能上传流程调用 |

补充说明（来自 `internal/types/custom_agent.go`）：

- `builtin-wiki-fixer` 与 `builtin-skill-installer` 不显示在用户可见的 Agent 列表（`builtinAgentIDsOrdered` 排除了它们）——前者由 Wiki 编辑器、后者由技能上传流程程序化调用，但仍可经 `GetAgentByID` 使用；
- `builtinAgentIDsOrdered` 中还保留了 `builtin-deep-researcher`、`builtin-knowledge-graph-expert`、`builtin-document-assistant` 等 ID 常量位次，但当前 YAML 未定义这些条目，注册表以 YAML 为准；
- `builtin_agents.yaml` 里除快速问答外的条目都带 `reflection_enabled`（数据分析师为 `true`，其余 `false`），但**后端目前不消费这个字段**——`internal/` 下既没有对应的结构体字段也没有引用，只有 YAML 与前端类型定义里存在。也就是说它当前不影响 Agent 的实际行为，看到它为 `true` 不要以为多了一轮反思。

顺带一提，`internal/agent/prompts_wiki.go` 中的 `WikiSummaryPrompt`、`WikiKnowledgeExtractPrompt`、`WikiTaxonomyPlanPrompt` 等常量属于 **Wiki ingest 管道**（文档入库时 LLM 生成 wiki 页面/目录规划）使用的提示词，与 wiki 类 Agent 的运行时工具互补：前者生产 Wiki 内容，后者消费与维护。

### 建议问题（Starters 与追问） {#_10-建议问题-starters-与追问}

对话框在两个位置会给出可点击的问题：会话还空着时的**开场问题**（starters），以及每轮回答结束后的**追问建议**（follow-ups）。这套配置归 Agent 所有（`QuestionSuggestionConfig`，`internal/types/custom_agent.go`），渠道设置只能抑制展示，不能改内容策略。

#### 配置项

两组配置各自独立开关，`mode` 决定问题从哪来：

| mode | 来源 | 适用 |
| --- | --- | --- |
| `curated` | 只用人工填写的 `items` | 开场问题 |
| `knowledge` | 从知识库内容里取 | 开场问题、追问 |
| `generated` | 让模型根据对话生成 | 追问 |
| `hybrid`（默认） | 上述来源混合 | 开场问题、追问 |

| 配置 | 默认 | 说明 |
| --- | --- | --- |
| `starters.enabled` / `mode` / `items` / `count` | 开 / `hybrid` / 空 / 6 | 开场问题；`count` 取 1–8 |
| `follow_ups.enabled` / `mode` / `count` | 关 / `hybrid` / 3 | 追问建议；`count` 取 1–5 |
| `follow_ups.model_id` | 空（用会话模型） | 生成追问用的模型，可指定小模型省成本 |
| `follow_ups.categories` | 三类全选 | 限定问题类型：`clarify`（澄清）/ `deepen`（深入）/ `action`（行动） |
| `follow_ups.max_context_turns` | 2 | 生成时回看几轮对话，取 1–5 |
| `follow_ups.additional_instruction` | 空 | 追加到生成提示词的业务约束，最多 2000 字符 |
| `follow_ups.suppress_on_fallback` | 开 | 回答走了兜底策略时不出建议 |
| `follow_ups.suppress_when_answer_asks_question` | 开 | 回答本身在反问用户时不出建议（避免两个问题打架） |
| `follow_ups.knowledge_fallback` | 开 | 生成失败时回退到知识库来源 |
| `follow_ups.allow_regenerate` | 关 | 是否允许用户手动换一批 |

与用户刚问的问题相同的追问（忽略大小写、空白和常见标点）会被剔除，知识库来源的候选会补足数量。用户点选来自知识库的建议问题后，智能推理模式会先检索该问题的来源知识库或文档再回答。

#### 生成、缓存与埋点

- 结果存 `message_suggestion_sets` 表，按 `(assistant_message_id, placement, config_hash, locale)` 缓存——`config_hash` 把「当前生效的 Agent 配置」摘要进缓存键，所以改了配置会自然拿到新的一批，而不是读到旧缓存；`locale` 让多语言各自缓存；
- 状态：`generating` → `ready`，另有 `suppressed`（按上面的抑制规则跳过）与 `failed`；`lease_until` 防止多实例重复生成同一批；
- 接口：`GET /sessions/:id/messages/:message_id/suggestions` 读，`POST` 同路径触发生成（幂等），`POST /sessions/:session_id/suggestion-events` 上报埋点；
- 埋点事件：`impression`（曝光）/ `click`（点击）/ `dismiss`（关掉）/ `regenerate`（换一批），存 `message_suggestion_events`。点击后发出的下一条用户消息会带 `SuggestionAttribution`（`suggestion_set_id` + `question_id`），因此统计上能区分「点了建议」与「自己打了同样的问题」。

## 执行机制参考

### 总览与架构 {#_1-总览与架构}

#### 核心组件 {#_1-1-核心组件}

| 组件 | 源码位置 | 职责 |
| --- | --- | --- |
| `AgentEngine` | `internal/agent/engine.go` | ReAct 主循环的驱动者，持有配置、工具注册表、Chat 模型、事件总线等 |
| `ToolRegistry` | `internal/agent/tools/registry.go` | 工具注册、查找、参数校验、执行、输出截断、资源清理 |
| 内置工具集 | `internal/agent/tools/*.go` | 按能力注册的内置工具 + 动态 MCP 工具 |
| Token 估算与压缩 | `internal/agent/token/` + `internal/agent/compaction/` | `Estimator`（BPE 估算）与长轮次上下文压缩（sandbox 工具历史） |
| 记忆整合 | `internal/application/service/memory/` | 跨会话长期记忆：抽取、召回、主题提升、文档亲和度、整理 |
| 技能系统 | `internal/agent/skills/` | SKILL.md 的发现、加载与脚本执行（Progressive Disclosure） |
| 执行沙箱 | `internal/sandbox/` | 技能脚本与 `shell_exec` 的 Docker / Cube / E2B 会话级隔离执行与安全校验 |
| 工具审批 | `internal/agent/approval/gate.go` | MCP 危险工具的人工审批（HITL）与会话内 OAuth 授权 |
| Agent 服务层 | `internal/application/service/agent_service.go` | 组装引擎：注册工具、解析 KB 元信息、初始化技能/沙箱/VLM |
| 会话问答入口 | `internal/application/service/session_agent_qa.go` | 从 `CustomAgent` 构建运行时 `AgentConfig` 并执行 |
| 历史重建 | `internal/application/service/agent_history.go` | 从 DB 重建多轮 LLM 上下文（`LoadAgentHistory`） |

`AgentEngine` 的结构体定义（`internal/agent/engine.go`，节选）：

```go
type AgentEngine struct {
	config               *types.AgentConfig
	toolRegistry         *agenttools.ToolRegistry
	chatModel            chat.Chat
	eventBus             *event.EventBus
	knowledgeBasesInfo   []*KnowledgeBaseInfo    // Detailed knowledge base information for prompt
	selectedDocs         []*SelectedDocumentInfo // User-selected documents (via @ mention)
	pinnedMCPServices    []*PinnedMCPServiceInfo // User @mentioned MCP services for this turn
	pinnedSkills         []*PinnedSkillInfo      // User @mentioned skills for this turn
	questionOrigin       *QuestionOriginInfo     // Source of a picked suggested question, if any
	memoryPrompt         string                  // Long-term memory envelope appended to the system prompt
	skillsManager        *skills.Manager         // Skills manager for Progressive Disclosure (optional)
	tokenEstimator       *agenttoken.Estimator   // Token estimator for context window management, calibrated
	compactor            *compaction.Compactor   // Summarizes older history to fit the context window (nil = disabled)
	checkpointSink       types.ContextCheckpointSink // persists compactions that end on a stored turn
	modelContext         *modelcontext.Registry  // single request-local boundary for every model handle
	steerSink            types.SteerSink         // lets users append messages into the running turn
	// ... 其余为估算校准、溢出恢复等运行期状态
}
```

引擎职责与约束：

1. **引擎跨轮无状态（stateless across turns）**。引擎源码注释明确写道：会话历史每轮由调用方通过 `service.LoadAgentHistory` 从 DB 重建，作为 `llmContext` 传入 `Execute`；引擎自身不维护缓存、system prompt 存储或跨轮缓冲。
2. **事件驱动输出**。引擎不直接写 SSE，所有输出（思考、工具调用、工具结果、最终答案、完成事件）都通过 `event.EventBus` 发射，由 Handler 层的订阅者转成 SSE 流并落库。相关事件类型包括 `EventAgentThought`、`EventAgentFinalAnswer`、`EventAgentToolCall`、`EventAgentToolResult`、`EventAgentTool`、`EventAgentComplete`、`EventError`。
3. **引用/资源别名**。`modelContext`（`modelcontext.Registry`，见 `internal/modelcontext/`）在每次 LLM 调用前对消息做 `EncodeMessages`，把持久化 ID（chunk/document/web 的 UUID）替换为短别名（`cN`/`dN`/`bN`/`wN`、`res://NNNN`），流式返回时再 Decode。这样模型永远看不到真实 UUID。编码顺序（资源句柄先于来源别名）固定在 `Registry` 内部、调用方无法反转（见 `registry.go` 的类型注释）：否则 wiki summary 页 slug 中内嵌的文档 UUID 会被 citation 压缩误替换为 `d1` 之类的别名，形成死链。
4. **可观测性**。每次执行会开启 Langfuse span 层级：`agent.execute` → `agent.round.N` → `agent.tool.<name>`，内含轮次、token 用量、工具输出预览（截断至 4000 rune）等。`database_query` 的 SQL 参数在 Langfuse 与 UI hint 中均被脱敏（`toolHintSensitiveArgs`）。

#### 组件关系图 {#_1-2-组件关系图}

```mermaid
flowchart TB
    subgraph HandlerLayer["Handler 层"]
        H1["session/qa.go AgentQA"]
        SSE["SSE 流 / agent_stream_handler"]
    end
    subgraph ServiceLayer["Service 层"]
        SQA["session_agent_qa.go<br/>buildAgentConfig + LoadAgentHistory"]
        AS["agent_service.go<br/>CreateAgentEngine / registerTools"]
    end
    subgraph EngineLayer["internal/agent"]
        ENG["AgentEngine<br/>（ReAct 主循环）"]
        TOK["token.Estimator"]
        MEM["compaction.Compactor"]
        REG["tools.ToolRegistry"]
    end
    subgraph Tools["工具集"]
        KB["KB 检索工具<br/>search_knowledge / read_document / list_documents"]
        WIKI["Wiki 工具 x9"]
        WEB["web_search / web_fetch"]
        DATA["data_schema / data_analysis（DuckDB）"]
        SKILL["read_file / shell_exec / 沙箱文件工具"]
        MCP["MCP 目录与按需加载工具"]
    end
    GATE["approval.Gate<br/>（HITL 审批 / OAuth）"]
    SBX["sandbox.Manager<br/>（Docker / Cube / E2B）"]
    EB["event.EventBus"]

    H1 --> SQA --> AS --> ENG
    ENG --> TOK
    ENG --> MEM
    ENG --> REG
    REG --> KB
    REG --> WIKI
    REG --> WEB
    REG --> DATA
    REG --> SKILL
    REG --> MCP
    MCP --> GATE
    SKILL --> SBX
    ENG --> EB --> SSE
```

#### System Prompt 的构建 {#_1-3-system-prompt-的构建}

`internal/agent/prompts.go` 的 `BuildSystemPromptSections` 先选择基础模板：显式正文优先，否则无知识库用 `pure`、有知识库用 `rag`；随后按顺序拼接中途补充、运行时约定、来源、工具、输出、技能、记忆和引用协议等段。技能段仅在具备 `read_file`、存在可用技能且不处于技能安装模式时加入。

当前轮的知识库摘要、固定文档、日期和会话信息由 `observe.go` 放入用户消息的 `runtime_context`，不持久化到历史；通用回答规则位于系统段。`@MCP` / `@Skill` 产生已授权资源的优先使用提示，不自动排除其他可用来源。

段顺序、占位符、模板引用保存与消息角色边界统一维护在[对话提示词拼装](../06-development/05-agent-prompts.md)。

### ReAct 循环逐阶段详解 {#_2-react-循环逐阶段详解}

#### 入口：Execute {#_2-1-入口-execute}

`AgentEngine.Execute`（`internal/agent/engine.go`）流程：

1. `defer e.toolRegistry.Cleanup(ctx)` —— 执行结束时清理实现了 `types.Cleanable` 的工具（如 `data_analysis` 会 DROP 本会话建的 DuckDB 表）；
2. 开启 Langfuse `agent.execute` span；
3. 初始化 `types.AgentState`（`RoundSteps`、`KnowledgeRefs`、`IsComplete=false`、`CurrentRound=0`）；
4. `buildSystemPrompt` + `buildMessagesWithLLMContext`（system + 历史 + 当前用户消息，附图片 URL）；
5. `buildToolsForLLM` 把注册表中的工具转换为 function calling 定义；
6. 进入 `executeLoop`。

#### 主循环：executeLoop 与 runReActIteration {#_2-2-主循环-executeloop-与-runreactiteration}

```go
for state.CurrentRound < e.config.MaxIterations {
    // ctx 取消检查 → 若已有工具结果则抢救性合成最终答案
    outcome, iterErr := e.runReActIteration(...)
    switch outcome {
    case iterOutcomeContinue: continue loop   // 空回复重试，不消耗轮次
    case iterOutcomeBreak:    break loop      // 终止（自然停止/卡死/取消/内容过滤）
    case iterOutcomeNext:     state.CurrentRound++
    }
}
if !state.IsComplete && ctx.Err() == nil {
    e.handleMaxIterations(ctx, query, state, sessionID) // 兜底合成最终答案
}
```

`executeLoop` 用 `defer emitCompletion()` 保证**每条退出路径恰好发射一次 `EventAgentComplete`**（使用 `context.WithoutCancel` 使用户点击"停止"后事件仍能送达），该事件携带 `state.RoundSteps`，由 stream handler 写到 assistant 消息的 `AgentSteps` 字段持久化。

一次迭代 `runReActIteration` 内部依次是四个阶段：

**① Think（思考）**：先做上下文窗口管理（见[记忆与上下文压缩](#_4-记忆与上下文压缩)），再把用户在运行中追加的 `inject` 消息写入历史并接到消息列表末尾（见[向运行中的回答追加消息](../04-api/02-api-chat.md#steer)），然后 `callLLMWithRetry`（`internal/agent/think.go`）：

- `agenttools.SanitizeMessages` 修复连续同角色、孤儿 tool result 等问题；
- 流式调用 LLM（`streamThinkingToEventBus`），连续 `defaultLLMStallTimeout = 120s` 没有任何输出才取消（可用 `AgentConfig.LLMCallTimeout` 覆盖），持续输出的长轮次不受总时长限制，总时长由模型传输层兜底；
- 瞬时错误（429/5xx/timeout/overloaded 等，见 `transientErrorMarkers`）最多重试 `maxLLMRetries = 2` 次，退避 1s、2s；
- 若重试仍失败但此前已有工具结果，走**优雅降级**：`streamFinalAnswerToEventBus` 基于既有工具结果合成最终答案，`state.IsComplete = true`。

流式过程中：`reasoning_content` 通道（DeepSeek 等）与内嵌 `<think>` 块（由 `ThinkStreamSplitter` 切分）都路由到"思考"区（`EventAgentThought`）；普通 content 直接乐观地流到最终答案区（`EventAgentFinalAnswer`），如果本轮随后发起了工具调用，这段文本会被 UI 视为 preamble 挪进步骤树，同时保留为该轮的 `Thought`。

**② Analyze（判定）**：`analyzeResponse`（`internal/agent/observe.go`）检查停止条件：

- `finish_reason == "content_filter"` 且无工具调用 → 终止，答案为被过滤的内容或固定的道歉话术；
- 自然停止（`isNaturalStopFinishReason`：`stop` / `end_turn` / `stop_sequence`）且无工具调用 → **Agent 结束**，纯文本回复即最终答案（**没有专门的 final_answer 工具**；历史数据中遗留的 `final_answer` 工具调用会在重放时被 `filterNonTerminalToolCalls` 过滤掉）；
- 自然停止但内容为空 → 追加一条 nudge 用户消息 `"Please provide your complete answer now as plain text."` 重试，最多 `maxEmptyResponseRetries = 2` 次（返回 `iterOutcomeContinue`，不消耗轮次）；重试期间不发出终态答案事件，重试耗尽才以固定 fallback 文案作为唯一的最终答案；
- 因输出上限截断（`finish_reason` 为 `length` / `max_tokens` / `max_output_tokens`）且有正文、无工具调用 → 交付截断前的正文并结束本轮，答案事件与 `AgentStep` 带 `truncated` 标记；截断时没有正文（只有思考内容）则按空回复重试；连续 `maxConsecutiveLengthRounds = 3` 轮截断（通常截在工具调用参数里）时停止，没有正文则返回固定提示，建议缩小问题或调大 `max_completion_tokens`；
- 即将自然停止时若有用户追加的 `inject` 消息，本轮回复作为中间回答保留，智能体读取追加内容后继续；此时已到迭代上限也允许多跑一轮（`maxSteerOverruns = 1`）。

另有一个**卡死检测**在 Analyze 之前：若连续 `maxRepeatedResponseRounds = 2` 轮返回完全相同内容（包括连续为空）且无工具调用（通常是未处理的 finish reason 导致），强制终止并把该内容作为最终答案，内容为空时使用固定 fallback 文案。

**③ Act（行动）**：`executeToolCalls`（`internal/agent/act.go`）执行本轮所有工具调用：

- `AgentConfig.ParallelToolCalls == true` 且调用数 ≥ 2 时用 `errgroup` **并行执行**（best-effort，单个失败不取消兄弟任务），结果按原顺序回填；
- 每个调用先 `NormalizeToolCallID`，然后解析 JSON 参数——解析失败会先经 `RepairJSON` 修复再试；仍失败则返回带提示的错误结果（`"[Analyze the error above and try a different approach.]"`），让模型换路子而不是让整轮失败；
- 单个工具执行超时 `defaultToolExecTimeout = 60s`；`shell_exec` 为 `shellExecToolTimeout = 10m5s`（略长于命令自身 600s 上限，以便返回结构化超时结果），`local_browser` 的人工接管步骤使用单独的等待时长；`ToolExecContext` 中额外携带不带该超时的 `ApprovalCtx`，供 MCP 人工审批/OAuth 等合法长等待使用；
- 发射 `EventAgentToolCall`（含中文 display name 的 hint，如 `搜索网页("...")`）、`EventAgentToolResult`、`EventAgentTool` 事件。工具执行失败同样以 `tool_result` 发给客户端（`success: false` 与 `error`），不再作为 `error` 事件，智能体会根据错误继续处理。

**④ Observe（观察）**：`appendToolResults`（`internal/agent/observe.go`）按 OpenAI 协议把本轮追加进消息数组：一条带 `tool_calls` 的 assistant 消息 + 每个结果一条 `role:"tool"` 消息（内容经 `modelContext.ModelToolResultForTool` 别名化）。若本轮任一成功的工具结果里含 Markdown 图片，还会向 system 消息追加一次 `## Retrieved Image Output Requirement` 要求（`internal/agent/image_requirement.go`），强制最终答案原样携带相关图片。随后 `state.CurrentRound++` 进入下一轮。

#### 终止条件汇总与最大迭代 {#_2-3-终止条件汇总与最大迭代}

| 终止路径 | 触发条件 | 最终答案来源 |
| --- | --- | --- |
| 自然停止 | finish_reason ∈ {stop, end_turn, stop_sequence} 且无工具调用、内容非空 | 该轮纯文本回复 |
| 空回复耗尽 | 自然停止但内容为空，nudge 重试 2 次仍空 | 固定 fallback 文案 |
| 输出截断 | 因输出上限截断且有正文、无工具调用 | 截断前的正文（带 `truncated` 标记） |
| 连续截断 | 连续 3 轮在输出上限处截断 | 最后一段正文或固定提示 |
| 内容过滤 | finish_reason == content_filter 且无工具调用 | 被过滤内容或安全提示 |
| 卡死检测 | 连续 2 轮相同内容且无工具调用 | 重复的内容本身 |
| 用户取消 / 超时 | ctx.Done()；若已有工具结果则抢救合成 | 合成答案或保留部分步骤 |
| LLM 不可恢复失败 | 重试耗尽；有工具结果 → 降级合成，否则报错 | 合成答案 / 错误事件 |
| 达到最大迭代 | `CurrentRound == MaxIterations`（`max_iterations` 为负数时不限轮数） | `handleMaxIterations` → `streamFinalAnswerToEventBus` 合成 |

最大迭代次数的多层默认值：

- 服务层 `ValidateConfig`：`0` 时兜底为 5，负数表示不限轮数，硬上限 `MAX_ITERATIONS = 100`（`internal/application/service/agent_service.go`）；
- `CustomAgent.EnsureDefaults`：未配置时为 10（`internal/types/custom_agent.go`）;
- 内置 Agent：智能推理 50、数据分析师 30、Wiki 问答/修订 30（`config/builtin_agents.yaml`）。

达到上限后 `handleMaxIterations` 通过 `internal/agent/finalize.go` 沿用当前消息列表，保留原消息角色、图片和工具调用配对，并追加收尾请求生成最终答案；这次调用不提供工具，设置 `tool_choice=none` 并关闭 thinking。

#### ReAct 循环流程图 {#_2-4-react-循环流程图}

```mermaid
flowchart TD
    START(["Execute 入口"]) --> INIT["构建 system prompt + 历史消息 + 工具定义"]
    INIT --> CHECK{"CurrentRound < MaxIterations？"}
    CHECK -- "否" --> MAXED["handleMaxIterations：<br/>用工具结果合成最终答案"]
    MAXED --> DONE(["EventAgentComplete"])
    CHECK -- "是" --> CANCEL{"ctx 已取消？"}
    CANCEL -- "是，且已有工具结果" --> SALVAGE["抢救合成最终答案"] --> DONE
    CANCEL -- "否" --> CTXMGMT["上下文窗口管理：<br/>按窗口保留预算压缩，必要时裁短工具结果"]
    CTXMGMT --> THINK["Think：注入追加消息后流式调用 LLM<br/>（120s 无输出超时，瞬时错误重试 2 次）"]
    THINK -- "失败且有工具结果" --> SALVAGE
    THINK --> STUCK{"连续 2 轮相同内容<br/>且无工具调用？"}
    STUCK -- "是" --> DONE
    STUCK -- "否" --> ANALYZE{"analyzeResponse 判定"}
    ANALYZE -- "content_filter" --> DONE
    ANALYZE -- "自然停止且内容非空" --> FINAL["纯文本回复 = 最终答案"] --> DONE
    ANALYZE -- "输出上限截断且有正文" --> FINAL
    ANALYZE -- "自然停止但内容为空" --> EMPTY{"空回复重试 <= 2？"}
    EMPTY -- "是" --> NUDGE["追加 nudge 用户消息<br/>（iterOutcomeContinue，不消耗轮次）"] --> THINK
    EMPTY -- "否" --> FALLBACK["固定 fallback 文案"] --> DONE
    ANALYZE -- "有工具调用" --> ACT["Act：执行工具调用<br/>（可并行，单工具 60s 超时）"]
    ACT --> OBSERVE["Observe：assistant+tool 消息入上下文，<br/>必要时注入图片输出要求"]
    OBSERVE --> NEXT["CurrentRound++"] --> CHECK
```

### 内置工具全解 {#_3-内置工具全解}

#### 工具总表 {#_3-1-工具总表}

工具名常量定义在 `internal/agent/tools/definitions.go`。下表覆盖全部内置工具（参数列只列 schema 中的字段，`*` 为必填）：

| 工具名 | 关键参数 | 行为 / 返回 |
| --- | --- | --- |
| `thinking` | `thought`\*、`next_thought_needed`\*、`thought_number`\*、`total_thoughts`\*、`is_revision`、`revises_thought`、`branch_from_thought`、`branch_id`、`needs_more_thoughts` | Sequential Thinking：记录/修订/分支思考步骤；返回思考进度（含 `incomplete_steps`），提示禁止在思考里出现工具名和最终答案 |
| `todo_write` | `task`、`steps[]`\*（`id`/`description`/`status`：pending/in_progress/completed） | 创建/更新检索类任务计划，仅限检索任务（总结交给 thinking）；返回格式化计划，`display_type: "plan"` |
| `search_knowledge` | `query`\*（一条自然语言问题或短语；keyword 模式下写精确词）、`mode`（`hybrid` 默认 / `semantic` / `keyword`）、`knowledge_base_ids[]`（`bN`，最多 10 个）、`limit`（默认 10，上限 30） | 唯一的知识库检索入口：`hybrid` 走向量 + 关键词的 RRF 融合，`semantic` 只走向量，`keyword` 由关键词索引（BM25 / 引擎关键词检索）提供，不再对 chunks 表做无索引的正则扫描；召回阈值与候选池取全局 `conversation.vector_threshold` / `keyword_threshold` / `embedding_top_k`（随附 `config.yaml` 为 0.2 / 0.3 / 30，未配置时回退 0.6 / 0.5 / 30），不读智能体自身的阈值；有 rerank 模型时重排（打分文本为「文档标题 + 分块正文」，FAQ 除外；阈值默认 0.3，全部未过阈值时保留得分 ≥ 0.15 的最佳候选），结果再做 MMR（λ=0.7）去冗；重排全部拒绝时结果带 `rerank_rejected` 并提示换 `keyword` 查标识符类词；结果带 `cN`/`dN` 短 ID，同一次调用内去重，同一文档的元数据头只输出一次；所选 KB 没有对应索引时按库降级而不是报错：`keyword` 遇到 FAQ 库或纯向量库改走语义检索，`semantic` 遇到纯关键词库改走关键词检索，结果里以 `requested_mode` 和 `mode_fallbacks` 标明哪些库降级及原因；只有作用域内没有任何分块索引（如全是仅 Wiki 的库）时才报错 |
| `read_document` | `id`\*（`dN` 文档句柄或 `cN` 分块句柄）、`offset`（阅读顺序中的位置，从 0 开始，不是 chunk 下标；翻页用返回的 `next_offset`）、`limit`（默认 20，上限 100）、`query`（文档内查找：按空白拆成多个词，分块须包含全部词，顺序不限、大小写不敏感）、`regex`（把整个 `query` 当一条正则，大小写不敏感）、`context`（`cN` 前后各带几个相邻分块，上限 5） | 始终先返回文档元数据头（标题、类型、parse_status、分块数、metadata），再按需返回分块：`dN` 从 `offset` 起分页遍历；`cN` 读取该分块并可带前后 `context`（邻居按 chunk_index 顺序取，不受父分块、摘要、图片分块占用的下标影响）；带 `query` 时即使 `id` 是 `cN` 也在其所属文档内查找，返回命中分块及前后各一块上下文（最多 20 处命中），无命中时附提示；单页或单次查找结果不超过工具输出预算的 80%，超出部分用 `next_offset` 续读；FAQ 条目按同样方式读取；校验 KB 在 searchTargets 内及 @mention 范围 |
| `list_documents` | `knowledge_base_id`\*（`bN`）、`keyword`（按标题或文件名子串过滤）、`page`（默认 1）、`page_size`（默认 20，上限 100） | 分页列出单个知识库的文档，返回可直接交给 `read_document` 的 `dN` 句柄 |
| `query_knowledge_graph` | `knowledge_base_ids[]`\*（1–10 个 `bN`）、`query`\* | 并发查询各 KB 知识图谱的实体与关系；只有当作用域内存在启用图谱的 KB 时才会提供给模型（`agent_service.go` 装配白名单时移除），能力要求 `all_of: [graph]` |
| `database_query` | `sql`\*（仅 SELECT） | 只读查询白名单表（`knowledge_bases`/`knowledges`/`chunks`），自动注入 tenant_id 过滤与 `deleted_at IS NULL`；SQL 参数在 UI/Langfuse 中脱敏 |
| `data_schema` | `knowledge_id`\*（`dN`） | 读取 CSV/Excel 文件的 `table_summary` + `table_column` 类型分块，返回列信息与行数；并告知模型该文档在 `data_analysis` 中固定以表名 `dataset` 访问 |
| `data_analysis` | `knowledge_id`\*、`sql`\* | 把 CSV/Excel 载入 DuckDB 后执行 SQL。文档由 `knowledge_id` 选定，SQL 中固定以表名 `dataset` 引用它（每次查询在独立连接上建临时视图映射到物理表，模型永远不需要在 SQL 里写文档 ID）；多 Sheet Excel 合并为一张表并暴露 `__sheet_name` 列；自动纠正列名大小写/空格差异；会话结束 Cleanup 时 DROP 所建表 |
| `web_search` | `query`\*，可选 `count`（不超过配置上限，最多 20）、`country`（两位国家码或 `ALL`）、`freshness`（`pd`/`pw`/`pm`/`py`，Brave 另支持日期区间）、`content` | 联网搜索，直接返回提供商的标题、摘要和 `wN` 页面短 ID；按任务需要选择知识库或联网检索，Agent 搜索不再自动进行 RAG 压缩；`country`/`freshness` 仅 Brave 与 Serply 支持，其他提供商传入时报错；`content=true` 在 15 秒内并行抓取前 3 条结果、各取 5000 字符正文摘录 |
| `web_fetch` | `items[]`\*（每项 `url`\*=`wN` 或 HTTP(S) URL，可选 `offset`、`limit`；`limit` 按字符计，默认且最多 8000，多项共享输出预算） | 并发抓取最多 8 个网页（SSRF 安全客户端 + DNS pinning，必要时 chromedp 渲染），直接返回 Markdown 或支持的文本正文；60s 超时。按字符分页，使用 `next_offset` 续读；完整正文保存在 `full_output_path`（`web://` 地址，仅同一会话可读），可用 `read_file` 跨轮按行读取；逐 URL 返回 `success`/`failed`/`skipped` 状态与可重试错误码（如快照已过期的 `snapshot_expired`），部分失败不影响其它页面 |
| `read_file` | `path`\*、`offset`（从 1 开始的行号）、`limit`（默认 2000 行）、`max_bytes`（上限 64 KiB，网页快照 50 KiB）；网页可带 `line_offset` | 读取工作区文本、skill:// 资源和 web:// 网页快照，按结果续读 |
| `shell_exec` | `command`\*；可选 `skill_name`、`work_dir`、`timeout_sec`、`stdin`（≤ 64 KiB）、`max_output_bytes`（默认 16 KiB，上限 64 KiB）、`max_stderr_bytes`（默认 8 KiB，上限 16 KiB）、`env` | 在当前会话沙箱运行命令，默认工作目录 `/workspace`；指定技能时解析技能目录和变量 |
| `list_sandbox_files` | `path`、`max_entries`（默认 200，上限 500） | 浏览沙箱文件和可用产物；仅在未注册 `shell_exec` 时提供 |
| `write_sandbox_file` | `path`\*、`content`\*、`mode`（`overwrite` 默认 / `append`） | 写入或追加工作区文件，不能写 `/workspace/input` |
| `edit_sandbox_file` | `path`\*、`edits`\*（每项 `old_string`\*、`new_string`\*、`replace_all`） | 基于原版本批量精确替换 |
| `search_memory` | `query`\*、`limit`（默认 10，上限 20） | 按当前调用者作用域查长期记忆 |
| `search_conversations` | `query`\*、`limit`（默认 5，上限 8） | 检索当前调用者的历史对话；范围由调用者身份决定，不接受范围参数 |
| `wiki_search` | `query`\*（大小写不敏感的 POSIX 正则，如 `stardust\|skyvault`；不是合法正则的文本如 `C++` 按字面匹配）、`regex`（`false` 强制字面匹配，`true` 要求合法正则）、`knowledge_base_ids[]`（`bN`）、`limit`（每个 KB 默认 10，上限 50）；旧参数 `queries[]`、`knowledge_base_id` 仍接受 | 在 Wiki 页面（标题/slug/别名/摘要/内容）上搜索，返回带 `bN` 标记的页面 slug 与摘要；之前已返回过的页面仍列出，但省略摘要 |
| `wiki_read_page` | `slugs[]`\* | 按 slug 读取 Wiki 页面全文、元数据、出入链（链接附摘要，已见的省略）；知识库按 slug 自动路由；`index` slug 返回按类型分组的目录概览（每类 top 20） |
| `wiki_write_page` | `slug`\*、`title`\*、`summary`\*、`content`\*、`page_type`\*、`aliases[]`、`source_refs[]` | 新建或整页覆盖 Wiki 页面；写入前规范化并校验 slug；自动处理出链 |
| `wiki_replace_text` | `slug`\*、`old_text`\*、`new_text`\*、`source_refs[]` | 精确文本替换，适合小修订 |
| `wiki_rename_page` | `slug`\*、`new_slug`\* | 重命名 slug 并级联更新所有引用它的页面链接 |
| `wiki_delete_page` | `slug`\* | 删除页面并自动清理其他页面上的入链，防止死链 |
| `wiki_flag_issue` | `slug`\*、`issue_type`\*（mixed_entities/contradictory_facts/out_of_date/other）、`description`\*、`suspected_knowledge_ids[]` | 标记页面事实错误/实体混淆等问题，记录 issue 供人工或自动维护 |
| `wiki_read_issue` | `issue_id` / `slug` | 查看某条 issue 详情或列出某页面的 pending issue |
| `wiki_update_issue` | `issue_id`\*、`status`\*（resolved/ignored/pending） | 更新 issue 状态 |
| `discover_mcp_tools` / `call_mcp_tool` | 发现：`mode`\*（`list_servers`/`list_tools`/`describe`/`search`）、`server_id`、`tool_name`、`query`、`cursor`、`limit`（1–50）、`refresh`；调用：`tool_ref`\*、`arguments`\* | 查询目录、读取完整定义并按需调用，见 [MCP 工具目录](08-mcp.md#mcp-tool-directory) |
| `mcp_...`（动态，含稳定哈希后缀） | 由 MCP 服务的 InputSchema 决定 | 读取定义后发布的外部函数；描述标明服务和原始工具名，执行时重新校验权限，可挂人工审批与会话内 OAuth |
| `local_browser` | `method`\*（`observe`、`snapshot`、`navigate`、`click`、`fill`、`tab_*`、`evaluate`、`request_help` 等），其余字段随 method 而定 | 操作用户已连接的本机浏览器；仅在请求开启 `local_browser_enabled` 且部署启用浏览器接入时注册，见[本机浏览器](../05-clients/09-local-browser.md) |
| `write_skill_file` / `edit_skill_file` | 写：`path`\*、`content`\*（≤ 256 KiB）；改：`path`\*、`old_string`\*、`new_string`\*、`replace_all` | 仅内置技能安装器在安装模式下使用，只能改动正在安装的技能目录 |

默认工具白名单 `DefaultAllowedTools()`（Agent 未配置 `allowed_tools` 时的回退）：`search_knowledge`、`read_document`、`list_documents`、`search_conversations`。`web_search` / `web_fetch` 与 `search_memory` 不由白名单决定：注册时先从白名单中剔除，再分别按联网开关、记忆开关（空间、用户、智能体三方都允许）注入。

**旧工具名的兼容**：`knowledge_search`、`grep_chunks` 已合并为 `search_knowledge`；`list_knowledge_chunks`、`get_document_info`、`wiki_read_source_doc` 已合并为 `read_document`。`definitions.go` 的 `legacyToolSuccessors` 记录这组映射，`NormalizeAllowedTools` 在注册工具时把已保存 Agent 配置、预设与 API 调用里的旧名字自动改写为新工具，无需数据迁移；历史消息中记录的旧工具名仍能正常渲染。

**文档阅读工具的可用范围**：`read_document`、`list_documents` 读取的是落库的分块，所有知识库无论索引策略都会写入分块，因此只要作用域内有向量/关键词库或 Wiki 库就会注册（`agent_service.go` 的 `documentToolSet`），供 Wiki 智能体回读原文；`search_knowledge` 仍要求向量或关键词索引。能力表里这两个工具标为 `Auxiliary`：它们能用于仅 Wiki 的库，但不会把仅 Wiki 的库拉进 RAG 智能体「全部知识库」的范围，派生 KB 过滤器时只有在没有其他知识库工具时才计入。`list_documents` 的 @文件 / @标签 范围在分页之前生效：标签下推到数据库过滤条件，指定文档直接按 ID 读取，`total_docs` 与 `next_page` 只统计范围内的文档。

**推荐的检索工作流**：`search_knowledge`（按问题选择 `mode`：默认 `hybrid`，精确词 / 报错信息 / 标识符用 `keyword`，改写或概念性问题用 `semantic`）→ `read_document`（按 `dN` 分页阅读上下文，或用 `query` 在文档内定位）→ 在答案里以 `cN` 句柄引用。Wiki 知识库则是 `wiki_search` → `wiki_read_page` → `read_document` 回读原始来源。

#### 工具注册表（ToolRegistry） {#_3-2-工具注册表-toolregistry}

`internal/agent/tools/registry.go`：

- **注册**：`RegisterTool` 采用 **first-wins** 策略——同名工具后注册者被拒绝，防止 MCP 服务通过名字碰撞劫持内置工具（对应安全公告 GHSA-67q9-58vj-32qx）；
- **定义导出**：`GetFunctionDefinitions` 按工具名排序，保证发给 LLM 的 tools 载荷跨请求字节级一致，以命中依赖前缀匹配的 provider prompt cache（如 Qwen 显式缓存）；
- **执行管线**：`ExecuteTool` = `CastParams`（把 `"true"` 转 `true` 等 LLM 常见类型偏差）→ `ValidateParams`（按 JSON Schema 预校验，省一次无效执行 + LLM 往返）→ `tool.Execute` → 输出截断；
- **输出截断**：`TruncateToolOutput`（`truncate.go`）默认上限 `DefaultMaxToolOutput = 24000` **rune**（可由 `AgentConfig.MaxToolOutputChars` 覆盖，`shell_exec`、`discover_mcp_tools` 等工具可声明更高的自身上限），超限保留头 70% + 尾 30%，中间插入截断标记，防止大结果污染上下文；
- **错误提示**：工具参数 JSON 无法解析时，返回结果追加 `"[Analyze the error above and try a different approach.]"`，引导 LLM 换策略；其他失败直接返回工具自身的错误信息；
- **清理**：`Cleanup` 遍历实现 `types.Cleanable` 的工具释放资源。

#### 能力（capabilities）机制与按配置启停 {#_3-3-能力-capabilities-机制与按配置启停}

`internal/agent/tools/capabilities.go` 是前端 `frontend/src/utils/tool-capabilities.ts` 的 Go 镜像，声明每个工具对 KB 能力的需求：

```go
var ToolCapabilityRequirements = map[string]ToolRequirement{
	"thinking":   {},
	"todo_write": {},
	"search_knowledge":      {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"read_document":         documentReaderRequirement, // AnyOf vector/keyword/wiki，Auxiliary
	"query_knowledge_graph": {AllOf: []KBCapability{CapGraph}, ConsumesFiles: true},
	"list_documents":        documentReaderRequirement,
	// 旧名保留各自原有的声明（wiki_read_source_doc 同 read_document），以便旧配置在归一化之前也能通过能力校验
	// ...
	"wiki_search":          {AllOf: []KBCapability{CapWiki}},
	// ...
	"data_analysis": {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
}
```

能力枚举为 `vector` / `keyword` / `wiki` / `graph` / `faq`。由此派生：

- `DeriveKBFilterForAgent(agentMode, allowedTools)`：Agent 编辑器/`@` 菜单里可选 KB 的过滤谓词；`quick-answer` 模式隐式要求 `vector|keyword`；
- `KBSatisfiesToolRequirements`：后端最后防线——绕过前端的客户端也无法把不兼容 KB 塞给工具；
- `ToolsConsumeFiles`：决定聊天输入框是否展示 `@file` 列表。

**运行时启停逻辑**（`agent_service.go` 的 `registerTools`）：

1. 起点是 `config.AllowedTools`（用户可编辑的白名单，preset 只做初始填充），先经 `NormalizeAllowedTools` 把旧工具名改写为新名；为空回退 `DefaultAllowedTools()`；共享智能体的只读调用、或作用域内没有可写 Wiki 库时，移除 Wiki 写工具（`wiki_write_page`、`wiki_replace_text`、`wiki_rename_page`、`wiki_delete_page`、`wiki_flag_issue`、`wiki_update_issue`）；
2. 若本轮**没有任何知识检索 scope**（Pure Agent 模式），过滤掉全部 KB/Wiki/数据工具；若同时未开 Web 搜索，连 `todo_write` 也一并去掉；
3. 按运行时开关注入：开启联网搜索时追加 `web_search` + `web_fetch`，记忆可用时追加 `search_memory`（白名单里写了也会先剔除）；
4. **硬安全网**：扫描 `SearchTargets` 中各 KB 的真实能力——没有 wiki KB 就丢弃全部 wiki 工具；没有 vector/keyword KB 就丢弃 `search_knowledge`、`query_knowledge_graph`、`database_query`，此时若也没有 wiki KB 还会丢弃 `read_document`、`list_documents`；没有启用图谱的 KB 就丢弃 `query_knowledge_graph`（防止配置陈旧：先勾了 wiki 工具、后换成非 wiki KB）；
5. 去重后逐个实例化并注册；MCP 工具按 `MCPSelectionMode`（all/selected/none）另行注册；沙箱 shell/文件工具按会话能力注册（有 `shell_exec` 时不再注册 `list_sandbox_files`），`read_file` 再叠加技能和网页数据源；`local_browser` 与技能安装器的文件工具走各自的注册条件；旧技能工具名仅作兼容识别，不再注册。

### 记忆与上下文压缩 {#_4-记忆与上下文压缩}

长期记忆按空间和调用者跨会话保存，与下述会话历史压缩分别配置。开启和个人管理见[跨会话长期记忆](23-memory.md)，完整接口见[记忆 API](../04-api/02-api-memory.md)。

#### Token 预算与估算器 {#_4-1-token-预算与估算器}

- 上下文预算：`AgentConfig.MaxContextTokens`，`buildAgentConfig` 未设置时兜底 `types.DefaultMaxContextTokens = 200000`；
- `token.Estimator`（`internal/agent/token/estimator.go`）用 tiktoken 的 **cl100k_base** 编码估算，常量 `perMessageOverhead = 3`、`perConversationTail = 3`；编码失败时退化为 `len(s)/4` 近似；
- **权威值优先**：真正的 token 数以模型 API 返回的 `Usage` 为准。引擎的 `estimateCurrentTokens` 用上一轮 API 报告的 `lastUsage.TotalTokens` 作基线，只对新增消息（assistant 回复 + tool 结果）做 BPE 增量估算；首轮无 Usage 时才全量估算。
- **估算校准**：cl100k 不是各家模型的分词器，中文在 cl100k 下约每字 1 token，而 Qwen、DeepSeek 约 0.6，英文与 JSON 则基本一致。估算器因此带一个校准系数（`Estimator.SetScale`，限制在 0.5–1.5），只作用于文本，每条消息的固定开销和图片的固定估值不乘。系数由引擎在同一轮对话的相邻两次请求之间测量：两次请求的工具定义、system prompt 和已有消息都相同，模型报告的 prompt token 增量只对应新追加的消息（上一次回复、工具结果、追加消息），用它除以这些消息的估算即得对话内容的系数，不受各家渲染工具定义方式的影响。中间发生过压缩或工具结果裁剪、样本含图片、比值不可信（< 0.3 或 > 3）、或累计样本不足 256 估算 token 时不计入。测得的系数随本轮 usage 存为 `context_token_scale`（即使模型没有报告 total token 也会保存）；本轮没测到系数（例如一次请求就结束、不调工具）时，沿用本轮开始时的系数，使最新一轮总带着最新的系数；下一轮 `LoadAgentHistory` 取最近一条带系数的消息，按它给历史计价并把系数交给引擎作为起点。加载器、首轮压缩判断和压缩器（保留原文预算、摘要输入上限）共用同一个估算器，因此同一把尺子：只校准压缩判断而不校准加载会让加载器先丢轮次。首轮压缩判断仍只计消息、不计工具定义。

#### 上下文压缩与溢出恢复 {#_4-2-上下文压缩与溢出恢复}

`manageContextWindow`（`internal/agent/observe.go`）在每轮 Think 前调用 `compaction.Compactor`。MaxContextTokens 优先取智能体配置，其次模型 parameters.context_window，最后回退 200000。触发阈值为窗口减去 reserve，reserve 至少 16384，并随本轮输出预算增加：`max(completion 预算 + 4096, 16384)`。

压缩按 Token 预算选择保留的最近消息，默认 KeepRecentTokens=20000，小窗口会压低到可用窗口的四分之一。长 ReAct 会话可以在当前轮内部切分；切点不拆开 assistant 工具调用与其 tool 结果，切分轮的前半段单独总结，以解释保留的后半段。

旧摘要参与更新，较老历史生成结构化摘要；结果以带标记的 user 消息放在 system 与保留尾部之间。摘要预算由 reserve、模型输出上限和保留预算计算，不再固定为 2000。摘要调用走流式接口，与引擎自身的对话轮次一样只设停顿超时：连续一段时间（默认 120 秒，随 `LLMCallTimeout`）没有任何输出才取消，总时长交给模型传输层兜底。此前的 60 秒总超时会掐断正在正常预填充和输出的大请求。模型的思考输出也算进展，但不计入摘要；流里报出的错误视为本次失败。每次摘要最多尝试 2 次，本轮已被取消时不再重试；失败时回退原始文本归档并标记 degraded。原始归档与摘要一样受摘要预算约束，从最新的消息往前保留并注明省略了多少条，保证退化时也能缩小上下文。

**摘要输入上限**。历史可以装到整个窗口，待摘要部分可能超出单次摘要请求能容纳的量。请求超窗会被拒绝并退化成原始归档，而退化结果不写压缩点，下一轮会原样再失败一次。因此 `Prepare` 只保留能装进一次请求的最新消息（窗口减去回复预算、上一次摘要和提示词，再留 10% 余量），并在提示里注明省略了多少条较早的消息；文件路径仍从全部待摘要消息中提取。省略条数记在 `Result.Omitted`，引擎日志里可见。

`internal/agent/compaction/fileops.go` 机械提取被压缩消息中的文件读写路径，并继承旧摘要的文件清单，避免模型忘记已经落盘的产物。普通读取使用 read_file，历史旧读取名仍可兼容识别。

若压缩后仍超预算，最后才裁短工具结果，工具结果预算取窗口的 20%，限制在 8192–32768 Token。没有可压缩内容或释放空间不足 5% 时，记下当前消息数量，避免在同一上下文大小反复花费模型调用。成功压缩会清除旧 usage 基线，并发出 context_compacted 事件，包含前后 Token/消息数、原因、split_turn 与 degraded。

**压缩点持久化**。当摘要的历史部分恰好结束在某个已落库轮次的末尾时，引擎把这段摘要作为压缩点（`messages.context_checkpoint`）写回该轮的 assistant 消息，下一轮直接从它开始，不再对同一段历史重复摘要。历史消息在重建时带上所属轮次的 assistant 消息 ID（`chat.Message.TurnID`，不上线），据此判断切点是否落在轮次边界。以下情况不写压缩点：切点落在某个已落库轮次内部（例如停在追加消息处）；摘要只覆盖当前轮。历史摘要回退成原始归档时仍然写入，并标记 `degraded`：不写的话，摘要器持续失败时每一轮都会重新加载同一段历史、再压缩、再失败；写入后下一次压缩会把它当作上一次的摘要交给模型重新整理。切分轮前半段的摘要不写入压缩点，因为该轮下次会被完整重放。压缩因释放不足 5% 被放弃时，本轮上下文保持不变，但只要历史部分给出了压缩点，照样写入，避免下一轮重复摘要同一段历史。写入只更新这一列，未匹配到该会话的 assistant 行时视为失败；失败时仅记日志，不影响本轮。历史的反向分页和按会话取最新压缩点都走索引 `idx_messages_session_created_id (session_id, created_at DESC, id DESC)`：取压缩点时从最新一条往回扫，遇到第一个压缩点即停。该索引（迁移 000106）用 `CREATE INDEX CONCURRENTLY` 创建，升级时不阻塞 messages 的写入；创建中断会留下 INVALID 索引，需删除后重跑迁移。压缩点存在被覆盖的那一轮上，所以会话分叉复制该轮时会一起复制，回滚或删除该轮时压缩点也随之失效。

提供商报告上下文超限（错误或响应截断判据）时，还可强制压缩并重试一次。仅因生成耗尽 completion 预算的截断不应误判成上下文超限。具体提供商错误识别见 `internal/agent/compaction/overflow.go`。

#### 会话历史（agent_history） {#_4-3-会话历史-agent-history}

跨轮历史由 `LoadAgentHistory`（`internal/application/service/agent_history.go`）每轮从 messages 表重建（DB 是唯一事实来源，无 Redis/内存缓存）：

- 历史按 Token 预算加载，不按轮数，`history_turns` 在 Agent 模式下不生效。预算是整个上下文窗口（`agent.HistoryTokenBudget`），刻意大于压缩阈值：加载器放不下的轮既不重放也不进摘要，等于丢失；若预算只到阈值，加载器会先裁掉旧轮，请求可能再也越不过阈值，压缩与压缩点都不会发生，会话退化成滑动窗口。预算到窗口时，超出阈值的部分交给首轮压缩摘要并写入压缩点，下一轮从新压缩点开始，历史随之回落；会话再次填满时才再压缩，压缩点之后的轮不会缺失，压缩也不会连续两轮发生；
- 会话中存在压缩点（见[上下文压缩与溢出恢复](#_4-2-上下文压缩与溢出恢复)）时，取最新的一个：它所在的轮及更早的轮由一条摘要消息代替，放在历史最前面，之后的轮原样重放。压缩点所在轮未被读到时（预算先装满），按 assistant 行的 `(created_at, id)` 顺序判断哪些轮在它之后。压缩点查询失败时退回无压缩点的历史；
- 按 `(created_at, id)` 从新到旧分页读取（每页 200 行，单次最多 5000 行），按 `RequestID` 配对 user/assistant，只保留 assistant 已完成（`IsCompleted`）的完整轮。读到压缩点或预算装满即停止，长会话不会整段读出。读取未到会话开头且最旧一行不是 user 消息时，该行所在的轮缺少原始问题，会被舍弃；
- 从最新的轮往前放入预算，遇到第一个放不下的轮即停止，保证保留的轮连续。最新一轮即使单独超出预算也会保留，由压缩负责切分。每轮按引擎实际发送的内容计价并返回（`agent.HistoryAsSent`）：未开启 `RetainRetrievalHistory` 时，历史中的 KB/Wiki 结果只以一行占位发送，也只按一行计入预算、只以一行留在内存里（`search_knowledge` 等结果入库时已压成一行，差异主要在按全文存储的 `wiki_read_page` / `wiki_search`）。每轮回放完就释放对应的数据库行，分页读取时同时驻留的约为一页数据库行加上要发送的历史；
- 每轮展开为：user 消息（含图片 caption 与附件 prompt；忽略 `RenderedContent` 快照，避免将旧渲染协议带入上下文）→ 每个含工具调用的 `AgentStep` 展开为 assistant(with tool_calls) + 若干 tool 消息 → 末尾一条规范化最终答案 assistant 消息（剥离 `<think>` 块）；
- 历史中的 tool 消息内容用 `CompactToolOutputForHistory`（`internal/agent/tools/persist.go`）压缩：带 `display_type` 的大载荷替换为一行摘要，如 `search_knowledge` 结果变为 `"Knowledge search returned N result(s) (details omitted from history)"`，`read_document` 的分块列表变为 `"Listed 20/87 chunks from X (content omitted from history)"`；`shell_exec`、`read_file` 等沙箱工具的结果按原结构重建而非压成一行。

进入引擎后，`buildMessagesWithLLMContext` 还会做**历史 KB 结果脱敏**（`redactHistoryKBResults`）：除非 Agent 开启 `RetainRetrievalHistory`，历史轮次中 KB 类工具（`search_knowledge`、`read_document`、`list_documents`、`query_knowledge_graph`、`wiki_search`、`wiki_read_page`，以及历史里可能残留的旧名 `knowledge_search`、`grep_chunks`、`list_knowledge_chunks`、`get_document_info`、`wiki_read_source_doc`）的结果一律替换为 `"[Previous retrieval result omitted — knowledge base may have changed. Please perform a fresh search.]"`，强制模型对可能已变更的知识库做新鲜检索。

持久化侧，`SanitizeAgentStepsForStorage` 在把 `AgentSteps` 写入 DB / SSE 重放前剥离 LLM-only 大载荷，只留紧凑摘要。

### 技能（Skills）系统 {#_5-技能-skills-系统}

使用步骤、安装来源、沙箱连接、网络策略和环境变量见[技能目录与沙箱](22-skills-sandbox.md)。技能依赖智能体选择的空间沙箱配置。

#### 渐进加载和作用域 {#_5-1-渐进加载和作用域}

技能包包含带 YAML frontmatter 的 `SKILL.md`，以及 scripts/templates 等资源。模型先看到名称和说明（Level 1），再通过 `read_file(path="skill://<name>/SKILL.md")` 读取完整说明（Level 2），按需读取附加资源（Level 3）。读取结果同时给出实际执行方式、可用文件和技能目录信息。

`skills_selection_mode` 为 all/selected/none；selected 由 selected_skills 指定。运行时仅暴露所选沙箱中已安装且可用的技能。`@技能` 只把已授权的提及记录为本轮优先项，不收窄原白名单，也不会授权一个原本不可用的技能。

统一入口为 `read_file` 和 `shell_exec(skill_name=..., command=...)`；旧 `read_skill`、`execute_skill_script` 不再注册。技能文件 URI 不是 shell 路径；执行包内脚本使用读取结果给出的目录或 `$WEKNORA_SKILL_DIR`。未选择空间沙箱配置时，脚本执行不可用；macOS 上的 Lite 桌面版改用[本机沙箱](22-skills-sandbox.md#lite-host)。

#### 会话环境与文件 {#_5-2-会话环境与文件}

Docker、Cube、E2B 都提供会话级沙箱。附件暂存、shell 执行和产物收集复用同一实例；沙箱身份绑定到会话，不能通过工具参数切换其他空间的运行环境。默认执行账号为沙箱内 root，隔离边界是沙箱本身。Docker 默认关闭，启用条件见[技能目录与沙箱](22-skills-sandbox.md#选择沙箱后端)。

| 路径 | 用途 |
| --- | --- |
| `/workspace/input` | 暂存聊天附件 |
| `/workspace` | 本轮或后续轮使用的工作文件、脚本 |
| `/workspace/output` | 可收集、预览和下载的交付文件 |
| `skill://<name>/...` | 技能包资源的读取地址 |
| `web://...` | 本会话持久化的网页快照，无沙箱时也可读取 |

沙箱空闲 TTL、技能镜像更新或重建会影响实例中的临时状态。对话产物收集见[会话与对话体验](18-chat-experience.md)，接口见[沙箱与技能 API](../04-api/02-api-sandbox-skills.md)。

#### 文件工具契约 {#_5-3-文件工具契约}

- **写入**：`write_sandbox_file` 只写 /workspace 下的文件，排除只读输入目录 /workspace/input；支持 overwrite/append，单文件最多 8 MiB。模型输出额度用于生成前预算，不作为拒绝完整文件内容的预测字节阈值。截断的工具调用在执行前拒绝，避免把半份内容写入文件。
- **读取**：`read_file` 使用从 1 开始的 offset 行号、limit 默认 2000 行，并受 max_bytes 和工具输出预算限制；截断时按返回的 next_offset 续读。工作区文本最多 64 KiB/页；网页快照最多 50 KiB/页，超长行使用 line_offset 续读。二进制不会直接作为文本返回。
- **修改**：`edit_sandbox_file` 接受 `edits:[{old_string,new_string,replace_all?}]`，所有匹配基于同一原始版本解析；匹配失败、歧义或区间重叠时整批拒绝，不部分写入。
- **并发**：append/edit 是读改写操作，按会话和文件路径串行化；不同路径仍可并行。
- **技能包**：普通工作区文件工具不直接修改已安装技能包。安装维护使用专门的技能写入工具，不作为普通 Agent 的通用文件编辑入口。

约束放在工具描述中，系统提示词仅说明选型和跨工具流程。底层文件缓存依赖会话、路径、大小、mtime 与文件变更纪元，避免同长度编辑后读到旧内容。

#### 执行流程 {#_5-4-执行流程}

```mermaid
sequenceDiagram
    participant LLM as Agent
    participant Reader as read_file
    participant Manager as 技能管理器
    participant Shell as shell_exec
    participant SBX as 会话沙箱
    LLM->>Reader: skill://name/SKILL.md
    Reader->>Manager: 校验已授权技能并加载说明
    Manager-->>LLM: 说明、文件列表、执行方式
    LLM->>Shell: skill_name + command
    Shell->>Manager: 解析技能运行环境和个人变量
    Shell->>SBX: 执行命令，默认工作目录 /workspace
    SBX-->>LLM: stdout / stderr / exit_code
    Note over SBX: 交付文件写入 /workspace/output
```

### 工具审批机制（Human-in-the-Loop） {#_6-工具审批机制-human-in-the-loop}

MCP 工具审批由 `internal/agent/approval/gate.go` 实现。

**审批范围**：审批门（`approval.MCPApproval`）**只接入 MCP 工具**——`MCPTool.Execute`（`internal/agent/tools/mcp_tool.go`）在真正调用 MCP 服务前询问 `gate.NeedsApproval(tenantID, serviceID, toolName)`；内置工具不走审批。哪些 MCP 工具需要审批由 `Checker`（DB 中的 `MCPToolApprovalService`，经 `approval.Adapter` 适配）按租户+服务+工具名判定。

**Fail-close 默认**：`NeedsApproval` 的检查器出错时默认**要求审批**（对 HITL 特性更安全）；可用环境变量 `WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN=true` 恢复旧的放行行为。

**审批流程**（`RequestAndWait`）：

1. 生成 `pendingID`（UUID），把 waiter 挂入内存 map；
2. 通过 EventBus 发射 `EventToolApprovalRequired`（携带服务名、MCP 工具名、参数 JSON、超时秒数、tool_call_id 等），前端弹出审批卡片；
3. 阻塞等待三者之一：用户 `Resolve`、超时（默认 **10 分钟**，`cfg.Agent.ToolApprovalTimeoutSeconds` 可配）、请求 ctx 取消；结果统一以 `EventToolApprovalResolved` 通知 UI；
4. `Decision` 支持 `Approved`、`Reason`，以及 `ModifiedArgs`——用户可在批准时**修改工具参数**，MCPTool 会用修改后的参数重新解析执行；
5. 拒绝/超时/取消都会作为工具失败结果返回给 LLM（而非中断整个 Agent）。

**长等待与超时的配合**：普通工具执行有 60s 超时，但审批可能等更久。引擎在 `ToolExecContext.ApprovalCtx` 中传入**不含** per-tool 超时的轮级 ctx 供审批等待使用；批准后 MCPTool 再从 `ApprovalCtx` 派生一个全新的执行超时窗口，避免审批耗尽预算导致刚批准就超时。

**跨实例支持**：waiter 存在发起等待的实例内存里；配置 Redis 后，`Resolve` 在本地未命中时通过 Pub/Sub 频道 `weknora:mcp_approval:resolve`（可加 `WEKNORA_REDIS_NAMESPACE` 后缀隔离多部署）广播到所有副本，由持有 waiter 的实例投递，并经带 nonce 的 per-pending 回复频道回 ack，使 HTTP 层能准确区分 `ok` / `not_found` / `tenant_mismatch` / `user_mismatch` / `already_resolved`。无 Redis 时退化为单进程语义（需要粘性会话）。

**授权校验**：`Resolve` 时校验 tenant 匹配；waiter 注册了 `userID` 时调用者必须携带相同的非空 userID（空视为不匹配，fail-close），防止旁人替会话主人批准。

**会话内 OAuth**：同一个 Gate 还提供 `RequestOAuthAndWait`——当 MCP 传输层返回"需要授权"错误时（而非查审批表），发射 `EventMCPOAuthRequired` 让用户在对话内完成 OAuth，等待上限取 Agent 配置的 `MCPAuthWaitTimeout`（`internal/agent/tools/mcp_oauth.go`），授权成功后自动重试工具调用。

### Agent 模式与普通 RAG 问答模式 {#_9-agent-模式与普通-rag-问答模式}

#### 两条问答路径 {#_9-1-两条问答路径}

路由层（`internal/router/routes_chat.go`）注册了两个入口：

```go
knowledgeChat.POST("/:session_id", handler.KnowledgeQA)  // /knowledge-chat/:session_id
agentChat.POST("/:session_id", handler.AgentQA)          // /agent-chat/:session_id
```

两者最终都汇聚到 `internal/handler/session/qa.go` 的统一执行流 `executeQA(reqCtx, mode, generateTitle)`，`mode` 二选一：

```go
const (
	qaModeNormal qaMode = iota // KnowledgeQA pipeline (RAG / pure chat)
	qaModeAgent                // Agent engine with tool calling
)
```

#### 模式决策逻辑 {#_9-2-模式决策逻辑}

`Handler.AgentQA` 按以下顺序选择执行模式：

1. 解析请求并经 `resolveAgent` 解析 `agent_id` 对应的 `CustomAgent`（含内置与共享 Agent 的权限校验）；
2. **`CustomAgent.IsAgentMode()` 优先于请求里的 `agent_enabled` 字段**——即 `Config.AgentMode == "smart-reasoning"` 才走 Agent，`quick-answer` 型 Agent 即使打到 `/agent-chat` 也会被降级：
3. 若 agent 模式成立但 `customAgent == nil`（典型场景：前端 localStorage 里 `selectedAgentId` 被清空但开关残留），提前返回 400 `"agent_id is required when agent mode is enabled"`，避免异步流里报晦涩错误；
4. 成立 → `executeQA(reqCtx, qaModeAgent, true)`；否则打日志 `"Agent mode disabled, delegating to normal mode"` 并走 `qaModeNormal`。

嵌入渠道（`internal/handler/embed_channel.go` 的 `delegateEmbedChat`）同理：`agentMode && ch.AgentID != types.BuiltinQuickAnswerID` 才转发 `AgentQA`，否则 `KnowledgeQA`。

#### 两条路径的差异 {#_9-3-两条路径的差异}

| 维度 | 普通 RAG（qaModeNormal） | Agent（qaModeAgent） |
| --- | --- | --- |
| 执行体 | KnowledgeQA chat pipeline（意图识别→改写→检索→rerank→拼 context→单次生成） | `AgentEngine.Execute` 的 ReAct 多轮循环 |
| 检索方式 | 管道固定的向量/关键词混合检索 | LLM 自主选择工具（语义/正则/图谱/Wiki/Web/SQL…），可多轮迭代 |
| 服务入口 | `sessionService.KnowledgeQA` | `sessionService.AgentQA`（**强制要求** `req.CustomAgent != nil`） |
| 历史 | 管道自身的多轮改写与历史拼装 | `LoadAgentHistory` 重建 assistant+tool 消息级历史 |
| 结果持久化 | 单条回答 | 回答 + `AgentSteps`（思考/工具调用树），SSE 可回放 |
| KB 兼容性 | 隐式要求 vector 或 keyword 索引（`quickAnswerKBFilter`） | 按 `allowed_tools` 的 capabilities 派生 |

`sessionService.AgentQA`（`internal/application/service/session_agent_qa.go`）在进入引擎前还处理：共享 Agent 的租户切换、视觉模型路由（模型支持 vision 则直传图片，否则把 VLM 描述并入 query）、引用上下文/附件内容并入 query、rerank 模型按需初始化等；执行是异步的，事件经 EventBus 流回 Handler 层。

### 关键常量速查 {#_11-关键常量速查}

| 常量 | 值 | 位置 |
| --- | --- | --- |
| `MAX_ITERATIONS`（服务层上限） | 100 | `internal/application/service/agent_service.go` |
| `defaultLLMStallTimeout` | 120s（连续无输出的上限，非总时长） | `internal/agent/const.go` |
| `defaultToolExecTimeout` | 60s | `internal/agent/const.go` |
| `shellExecToolTimeout` | 10m5s | `internal/agent/const.go` |
| `maxLLMRetries` | 2 | `internal/agent/const.go` |
| `maxEmptyResponseRetries` | 2 | `internal/agent/const.go` |
| `maxRepeatedResponseRounds` | 2 | `internal/agent/const.go` |
| `maxConsecutiveLengthRounds` | 3 | `internal/agent/const.go` |
| `DefaultMaxToolOutput` | 24000 rune（头 70% / 尾 30%） | `internal/agent/tools/truncate.go` |
| `DefaultMaxContextTokens` | 200000 | `internal/types/agent.go` |
| `DefaultReserveTokens` | 16384（输出预算较大时增加） | `internal/agent/compaction/settings.go` |
| `DefaultKeepRecentTokens` | 20000（小窗口下调） | `internal/agent/compaction/settings.go` |
| 审批默认超时 | 10 分钟 | `internal/agent/approval/gate.go` |
| shell_exec 默认超时 | 120s，上限 600s；资源限额取沙箱后端配置 | `internal/agent/tools/shell_exec.go` |
| 技能命名限制 | name ≤ 64、description ≤ 1024 | `internal/agent/skills/skill.go` |

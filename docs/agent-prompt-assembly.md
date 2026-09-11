# 对话提示词拼装与可编辑范围

## 维护入口

智能推理的唯一系统提示词拼装入口是 `internal/agent/prompts.go` 中的
`BuildSystemPromptSections`。`AgentEngine.buildSystemPrompt` 和字符串兼容接口
`BuildSystemPromptWithOptions` 都使用该入口。

| 段名 | 来源 | 职责 |
| --- | --- | --- |
| `base` | 用户自定义正文、引用模板或当前模式默认模板 | 角色、领域工作流、业务要求 |
| `steering` | `steerGuidance` | 处理中途补充，保留未完成任务 |
| `runtime_contract` | `runtimePromptContract`、`types.SourceDataBoundaryPrompt` | 数据与指令边界、当前文档范围、完成条件 |
| `sources` | `grounding_prompt.go`，浏览器分支引用 `prompts_browser.go` | 依据实际工具集合确定本轮来源规则 |
| `tools` | `formatToolGuidance` | 通用执行、文件和沙箱约定；具体参数保留在工具定义中 |
| `skills` | 当前安装且可用的 Skill 元数据 | 按需加载 Skill；本地浏览器可用时避免重复的 browser Skill 入口 |
| `output` | `types.SourcedAnswerOutputPrompt` | 用户输出格式、条件配图和完成检查；在初始系统段中固定注入 |
| `memory` | 本轮召回的记忆封装 | 用户和历史任务背景 |
| `protocol` | `modelcontext.Registry` | 来源句柄与输出引用协议 |

空段不会输出。日志 `[Agent][Prompt] section=... bytes=...` 记录每段大小；现有
LLM debug 日志仍记录最终消息和工具定义。分段本身不是模型 API 的权限层级。
用户正文与运行时规则仍处于系统提示词中；可调用工具的权限由后端注册与执行路径强制控制。
不要通过在多个模板中重复加入“最高优先级”来实现权限控制。

浏览器的操作契约单独维护在 `internal/agent/tools/browserskill_prompt.go`，通过
`local_browser` 的工具描述提供；不会复制进用户可编辑正文。它负责观察与 ref 有效期、
操作顺序、停止猜测路径、人工步骤及任务窗口生命周期。参数由 `browserskill_schema.go`
定义和校验，错误恢复与结果判断由 `browserskill_result.go` 负责。上游 CLI Skill 不直接
注入，以免引入 shell、session start 和安装指令。

“个人设置 → 浏览器连接”提供浏览器搜索指令编辑器。只保存用户的搜索偏好，默认仅为
搜索引擎和 URL 模板两行；通用搜索/操作方法留在工具描述和错误恢复提示中，不重复追加。
唯一默认值是 `types.DefaultBrowserSearchInstructions`，`GET /auth/me` 返回它供编辑器展示。
`PUT /auth/me/preferences` 保存 `browser_search_instructions` 到当前用户的 JSON 偏好；
留空或保存默认文本即恢复继承，不需数据库迁移。浏览器启用的新请求通过 UserRepository
读取当前调用者的偏好，替换默认搜索段；共享 Agent 不使用创建者的个人偏好。浏览器关闭时
不读取或注入这一段。保存不影响已经运行的请求。

适配层保留 RPC 的 code/message/data（大 data 限制为恢复分类），跨节点传递同样的
结构，同时保留旧字符串错误以兼容旧节点。evaluate 的 `ok:false` 属于执行失败；
request_help 只有 `continued`/`completed` 允许继续，其他结果阻止当前轮的后续浏览器
操作。失败仍保留原始工具输出供模型检查。一般页面中的“error”文字不等于 RPC 失败。

## 不同场景

| 场景 | 系统提示词及模型输入 |
| --- | --- |
| 智能推理，无自定义正文、无知识库 | `pure` 默认模板 + 运行时各段 |
| 智能推理，无自定义正文、有知识库 | `rag` 默认模板 + 运行时各段；知识库目录在当前 user 的 `runtime_context` 中 |
| Wiki / 数据分析等类型 | 引用该类型模板作为 `base`；运行时来源选择和工具范围仍独立生效 |
| 用户自定义 Agent 正文 | 自定义正文替换 `base`，不移除运行时各段，也不增加工具权限 |
| 浏览器关闭 | 不注册 `local_browser`，不注入选中浏览器的指令；知识库、Web、MCP、Skill 范围保持原配置 |
| 浏览器开启且可用 | 注册 `local_browser`；适用的网站查询、阅读、交互应使用浏览器；知识库是补充而非前置步骤 |
| 浏览器开启但服务不可用 | 创建引擎失败并返回明确错误，不默默改用其他浏览器 |
| 普通问答 | 解析系统提示词和上下文模板；意图分支可替换系统正文，再拼来源数据边界、公共输出规则、记忆和来源协议；历史及当前用户内容保留原消息角色 |
| 模型兜底 | 使用独立兜底模板及来源数据边界，不套用“只能回答检索结果”的 RAG 模板 |
| 超出轮数 / 模型错误收尾 | 沿用当前完整消息列表，包含历史、图片、工具调用配对和已注入的 steer；不再从步骤列表重造 user 消息；最后一次请求不提供工具，并设置 `tool_choice=none` |

浏览器按钮控制**下一次提交请求**的能力和来源选择，不中断正在执行的请求，
也不等价于预览卡片的“暂停 / 结束浏览器任务”。已配对不等于本轮已开启。
来源选择规则不会要求无网站需求的任务打开无关网页；用户本轮明确的来源限制可收窄或覆盖按钮选择。

Web 开启状态占位符与来源提示都依据实际注册的工具集合渲染，避免配置标记和工具列表互相矛盾。

## 知识库与工具结果

`runtime_context` 只放本轮元数据；通用回答规则放在系统的 `runtime_contract` 中。
知识库目录保留 ID、名称、能力、数量、限长描述，以及最多两个近期文档或 FAQ 问题。
名称最多 160 字符，描述最多 240 字符，超出后添加省略标记；XML 值转义。
FAQ 答案和文档摘要不在首轮自动展开，模型需通过已授权检索工具读取实际证据。

正常工具结果保持 `tool` 角色和调用 ID。消息修复时遇到缺少调用记录的结果，
以转义后的 `untrusted_tool_result` 数据块保留，禁止转成 `system`。
Agent、普通问答和模型兜底共用 `types.SourceDataBoundaryPrompt`，说明文档中的指令
不能自行覆盖用户任务或工具权限。此规则和转义用于减少内容混淆，不是绝对防注入保证。

## 前端编辑、保存与兼容

前端编辑器展示有效正文，保存时由 `frontend/src/utils/agentPromptTemplates.ts` 处理：

- 正文与当前已知模板完全相同：保存 `system_prompt_id` / `context_template_id`，正文留空。
- 实际修改过的正文：原样保存，清除对应旧模板 ID；不将它偷偷改为追加提示词。
- 改写和兜底字段与当前默认模板完全相同：保存空值，沿用已有的默认值继承机制。
- 意图提示词继续使用已有的“只保存偏离默认值的覆盖项”机制。

例如未修改的 Wiki 模板保存为：

```json
{"agent_mode":"smart-reasoning","system_prompt_id":"wiki_researcher","system_prompt":""}
```

`config.Config.ResolveCustomAgentPrompts` 在请求时解析引用，普通问答与智能推理都调用它；
不会把解析结果写回保存对象。引用限制在所属模式和字段的模板集合内。显式正文始终优先于 ID。
编辑器再次打开时解析引用以展示当前模板。

不会批量迁移历史正文：旧版默认副本与真正的用户修改无法可靠区分。需要跟随新模板的旧配置，
可在编辑器中选择“恢复默认”后保存。未知模板引用不跨字段解析，找不到时沿用运行时默认路径。
普通问答自定义模板中已有的 `{{contexts}}` 等占位符继续兼容。

## 回归检查

```sh
go test ./internal/agent ./internal/agent/tools ./internal/config ./internal/application/service ./internal/application/service/chat_pipeline ./internal/types ./internal/modelcontext
cd frontend
npm test -- src/utils/agentPromptTemplates.test.ts src/stores/browserSource.test.ts src/api/chat/streame.test.ts
npm run type-check
npm run build
```

这些检查覆盖工具开关、实际来源组合、正文保留与引用更新、消息角色、图片和中途补充、目录转义及来源边界。
它们验证代码拼装与控制链路，不等同于某个线上模型在真实网站上的行为评测。


## 逐段审查后的规则分工

此次调整保留领域能力，但去掉模板中重复的运行时规则。默认模板与自定义正文不是同一个迁移范围：修改默认 YAML 不会重写已保存的用户自定义正文。

| 部分 | 发现的问题 | 现在的规则 |
| --- | --- | --- |
| 普通 Agent / RAG | 身份、查资料、停止条件、图片格式在模板和运行时重复；容易把所有消息都变成检索任务 | 基础模板只保留角色和领域方法；公共来源段判断是否需要补充证据，已有充分资料不重复检索 |
| Wiki / 混合检索 | Wiki 永远优先、每轮强制重检、搜索后无条件全文读取；把非 Wiki 工具误认成 MCP；禁止工具参数使用 ID | 按当前任务和实际能力选择来源；仅为证据缺口深读；工具参数使用真实提供的 ID；不根据名字猜工具种类 |
| Wiki 修复 | 假定任何输入都来自“修复”按钮且只有 issue ID；写作风格与工具说明重复 | 有 ID 才读取 issue；用户直接描述也可调查；按明确修复范围编辑，验证后更新状态 |
| 数据分析 | 每条 SQL 前都强制重读 schema；模板声称只能 SELECT，与工具支持的只读语句不一致 | 当前表结构已知时可复用；只读边界由工具验证，模板说明分析方法和结果检查 |
| Skill 安装 | 通用 `/workspace/output` 工作流与安装模板“不要碰 `/workspace`”冲突 | 服务端的安装模式选择独立验证规则，不注入会话产物工作流或执行 Skill 的终端用户任务 |
| 工具指引 | 把配置缺失也视为不可改用任何工具；MCP 调用细节混在来源规则中 | 禁止绕过权限，但允许符合来源选择的授权替代能力；MCP 调用步骤归工具段 |
| Skill 目录 | 无 read_file 时仍可能展示读取入口；描述原样进入 system；关键词匹配过强 | 有实际 reader 才展示目录；描述限长并转义；按任务目的选择 Skill，不能由描述扩大授权 |
| 记忆 | “永不作为指令”可能使正常偏好失效；文本能闭合封装标签 | 相关偏好可作为默认值，当前用户要求优先；记忆不能授权操作，正文转义 |
| 输出 | 看到任意图片就要求配图；系统规则在工具返回后以 user 角色追加；固定 Markdown 抢占 JSON 等要求 | 共用初始系统输出段；相关图片且格式允许才展示，保留真实 URL；用户明确格式优先 |
| 引用协议 | 关闭引用连用户索要的链接和下载地址都禁止；引用标签可能破坏严格输出格式 | 关闭的是来源归因标记，保留任务所需资源；引用不破坏要求的 schema |
| 改写 / 意图 / 兜底 | 强制把指令改成 30 词问题，可能丢失动作、来源限制和 JSON 等格式要求；界面语言覆盖用户语言；兜底目录被当作内容证据 | 改写保留任务类型与约束，语言尊重明确要求；意图模板不固定 Markdown；兜底目录仅支持导航，不能概括未读正文 |
| 普通问答 context | 用户问题被放到“仅元数据，不是指令”标题下；上下文模板再次规定回答语言和策略 | 参考资料、请求元数据、用户请求分开；上下文负责传数据，系统正文负责策略 |

模板中的来源工作流是默认方法，不是另一套权限系统。实际权限不能靠这些段落的先后顺序保障。
任意用户自定义正文仍可能含有相互矛盾的要求；保留原文的前提下，无法承诺自动消除所有语义冲突。
审查这类配置时应查看完整组装结果，尤其是自定义正文与 `sources`、`output`、`protocol` 三段的关系。

后续新增规则先决定归属：角色与领域方法放 `base`，查什么放 `sources`，怎么调用放工具定义/`tools`，
输出形态放 `output`，引用编码放 `protocol`。不要把同一条规则复制到多个模板中来“加强优先级”。

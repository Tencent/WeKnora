# Agent 执行权限模型

状态：后端第 1–4 步已实现；`automation` 作用域与 `ActingVia` 已就绪，等待自动化需求接入；MCP 风险管理界面暂缓（§6）。日期：2026-10-19。实现与原设计的差异见 §7。

本文统一 Agent 运行时“哪些动作可以直接执行、哪些要用户确认、哪些拒绝”的判断。助手、多 Agent 协作尚未上线，借此机会收敛规则；自动化（`agent-automation.md`）直接建立在本模型之上，不再自带权限概念。

## 0. 已确认的决策

| 问题 | 决策 |
|---|---|
| 档位适用范围 | 从仅内置助手扩展到所有 smart-reasoning Agent |
| 普通 Agent 默认档位 | 等价 `auto_write`：MCP 仍只按租户逐工具开关审批，保持 #1173 已上线行为 |
| 助手默认档位 | 保持 `confirm`，受工作空间上限约束 |
| 文档组织 | 本文独立维护；自动化文档引用本文 |

## 1. 现状

“这个工具调用要不要等用户”目前由多处各自判断：

| 位置 | 作用对象 | 判断依据 |
|---|---|---|
| `operations.Access.NeedsApprovalFor` | 助手 operations 目录 | 风险等级 + 助手档位 |
| `tools.ConfirmedTool` | 助手直接调用的 Wiki 写工具 | 同上；非交互运行时直接拒绝 |
| `approval.Gate.NeedsApproval` | MCP 工具 | 租户逐工具 `MCPToolApproval.RequireApproval`；协作委托时被覆盖 |
| `hitl.Delegation.RequireApproval` | 协作子任务 | `Access != auto_write` 时子任务所有 MCP 调用都要审批 |
| `AgentCollaboration` 的工具过滤（`agent_collaboration.go`） | 模板子任务的其他写工具 | 非 `auto_write` 时直接报错，提示“需要在父对话审批” |
| `Interactive`、`Suspendable`、`WithUnattendedRun`、`WithMCPOAuthNonInteractive` | 运行模式 | 四个独立标记，由各工具分别解读 |

由此产生的问题：

1. **档位只作用于内置助手。** `GrantsOperationCatalog` 仅对 `BuiltinWeKnoraAssistantID` 成立；普通 Agent 无档位概念。
2. **只读档位不覆盖 MCP。** `read_only` 只清空 operations 写操作、Wiki 可写范围和 `fill_page`；MCP 写工具仍只看租户开关。
3. **父子规则不一致。** 同一用户、同一档位、同一 MCP 工具，父任务可能直接执行，子任务每次都要审批，包括只读查询。
4. **MCP 没有风险分级。** 只有布尔开关，`confirm` 与 `auto_write` 对 MCP 没有意义；MCP 规范中的 `readOnlyHint` / `destructiveHint` 注解未被使用（`mark3labs/mcp-go` 已解析 `Tool.Annotations`）。
5. **授权记忆单一。** `AllowInSession` 仅用于带 Grant 的 Wiki 写工具；MCP 审批无法记忆，子任务不继承，也没有可长期保留的授权。
6. **身份只认在线 Web 用户。** operations 依赖 `OperationIdentityFromContext`；后台运行只能构造一个看起来在线的 Web 用户，审计无法区分。

## 2. 模型

权限分三层，前两层保持现状，第三层统一：

1. **能力面**：Agent 能看到哪些工具。`allowed_tools`、MCP 启用开关、Skills、内置助手专属能力。不变。
2. **资源权限**：此用户能碰哪些资源。路由层 RBAC、知识库可写范围、共享 Agent 只读。不变，且每次运行和恢复时以真实用户重新校验。
3. **执行策略**：可用且有资源权限的动作，此刻能否不经确认直接执行。由唯一的决策函数给出：

```go
Decide(ctx, Action) Decision // allow | ask | deny，附原因与命中的授权
```

所有工具入口（operations、内置写工具、MCP、委托子任务）都调用它；除它之外不再有分散的审批判断。

### 2.1 动作描述

```go
type Action struct {
    Kind     string // operation | builtin | mcp
    ID       string // operation ID、工具名，或 mcp:<service_id>/<tool_name>
    Risk     Risk   // read | probe | write | destructive
    Always   bool   // 每次都确认，授权不可豁免
    Resource string // 规范化的资源范围，例如 knowledge_base:<id,...>；无则为空
    Summary  string // 审批卡展示
}
```

授权键 = `Kind + ID + Resource`，用于匹配已有授权。

风险来源：

| 动作类型 | 风险 | `Always` |
|---|---|---|
| operations | 目录中的 `Risk` | 目录中的 `AlwaysConfirm` |
| 内置写工具 | 现有 `assistantWriteToolRisk`，扩展到所有 Agent 的写工具 | 否 |
| MCP 工具 | 管理员逐工具配置；未配置时取注解：`readOnlyHint=true` → read，其余 → write | `RequireApproval=true` 映射为 `Always` |

MCP 注解来自外部服务，只用作默认值，规则如下：

- 管理员的显式配置（`mcp_tool_approvals.risk`）优先，destructive 只能由管理员设置。`destructiveHint` 不被采用：write 已经需要按档位确认，外部服务声明 destructive 只会让审批更严格，却无法区分声明是否可信，因此交给管理员判断。
- 未显式配置时，生效风险取“本次声明的风险”和“历史观察到的最高风险”（`observed_risk`）中较高者。`observed_risk` 只升不降：服务把注解从 read 改为 write 立即生效；从 write 改回 read 不生效，需要管理员显式设为 read。这取代了原设计中的“降级需管理员确认”流程，无需额外的待确认状态。

### 2.2 档位

`AssistantAccessLevel` 更名为执行档位（类型名可保留，避免无谓的改动），适用于所有 smart-reasoning Agent：

| 档位 | read / probe | write | destructive 或 `Always` |
|---|---|---|---|
| `read_only` | allow | 不注册 | 不注册 |
| `confirm` | allow | 命中授权则 allow，否则 ask | ask |
| `auto_write` | allow | allow | ask |

生效档位 = min（用户本次选择，Agent 上限，工作空间上限）。

- 内置助手：沿用用户在面板选择的档位，默认 `confirm`。
- 普通 Agent：Agent 配置新增执行档位上限，默认 `auto_write`；用户未选择时取该上限。因此在未调整配置的情况下，普通 Agent 的 MCP 仍只在 `RequireApproval=true`（即 `Always`）时审批，与现状一致。
- `read_only` 在注册阶段移除写工具，包括 MCP 写工具和 Wiki 写工具；仍到达 `Decide` 的写操作（例如注册后风险变化）返回 deny，不再 ask。
- 每个 Agent 的上限保存在 `CustomAgentConfig.AccessLevel`（空值即 `auto_write`），编辑界面可配置；内置助手使用工作空间的 `MaxAccessLevel`。

### 2.3 运行模式

四个标记合并为一个枚举，放在运行上下文中，由运行入口设置：

| 模式 | 来源 | `ask` 的处理 | `ask_user` | MCP OAuth |
|---|---|---|---|---|
| `attended` | Web 对话、助手面板 | 弹审批卡；可挂起，刷新后恢复 | 可用 | 弹授权卡 |
| `deferred` | 自动化 | 挂起运行，写通知；用户在会话中处理后恢复 | 不注册 | 失败并说明 |
| `unattended` | API、嵌入渠道、IM 等无法处理卡片的渠道 | deny，附原因 | 不注册 | 失败并说明 |

现有 IM 渠道保持 `unattended`；后续若 IM 支持审批卡，再单独切换。

### 2.4 授权

统一为一张表，替代现在依赖 `agent_prompts` 中 `AllowInSession` 响应的隐式查询：

| 字段 | 说明 |
|---|---|
| `id, tenant_id, user_id` | 授权属于用户本人；`user_id` 是 `Principal.StorageID()` |
| `grant_key` | 授权键，见下文 |
| `label` | 审批卡上展示的授权范围，用于查看与撤销界面 |
| `scope` | `session` / `automation` |
| `scope_id` | 根会话 ID 或自动化 ID |
| `prompt_id` | 产生该授权的审批 |
| `binding_hash` | 仅 `automation`：授权前提的哈希，见下文失效规则 |
| `expires_at, revoked_at, created_at, updated_at, last_used_at` | |

表为 `agent_grants`，唯一键为（`tenant_id, user_id, scope, scope_id, grant_key`）；再次批准同一授权时复用该行并清除 `revoked_at`。授权键：

- MCP 写工具：`mcp:<service_id>/<tool_name>#<输入 schema 的 sha256 前 6 字节>`。服务改变工具参数后键随之变化，旧授权自然不再匹配。
- Wiki 等内置写工具：沿用工具原有的 Grant（工具名 + 规范化的知识库范围）。

规则：

- 可豁免的范围按作用域区分，`Always` 在任何作用域都不可豁免：

  | 风险 | `session` | `automation` |
  |---|---|---|
  | write | 可，运行中在审批卡上勾选“本会话允许” | 可，创建或编辑自动化时预先授权 |
  | destructive | 不可 | 可，仅限创建或编辑自动化时预先授权，见下文 |
  | 任意风险且 `Always` | 不可 | 不可 |

- destructive 的自动化授权用于“每晚清理过期文档”这类必然包含删除的任务；没有它，这类自动化每次运行都会挂起。约束如下（尚未实现，随自动化需求一起实现，见 §4）：
  - 只能在自动化的设置表单中逐项勾选并通过审批卡批准；运行中的审批卡不提供“以后都允许”，模型也不能代为授予。
  - 必须指定资源范围：带资源的动作（例如知识库文档删除）按具体知识库授权，不能对“所有知识库”授权；MCP 工具没有资源概念，按工具加参数 schema 授权。
  - 审批卡把 destructive 授权与 write 授权分开列出并标明“不可撤销”，需要单独确认。
  - 授权表增加 `risk` 列，记录批准时动作的风险。授权只覆盖不高于该风险的调用：write 授权不会因为工具被调高为 destructive 而继续生效。
  - 失效规则与其他 `automation` 授权相同（§2.4 下文）；撤销、过期或失效后，下一次运行在该调用处挂起询问。
  - 决策日志与审计照常记录；命中 destructive 授权的调用另记 `agent_granted_destructive`，方便事后排查。
- `session`：审批卡上的“本会话内允许”，扩展到 MCP 写工具。`scope_id` 始终是**根会话** ID：根任务和委派子任务上批准的授权都写到根会话名下，子任务判断时按根会话匹配。授权随根会话结束，用户删除会话时一并清理。不设单独的协作作用域，原因是每次用户发新消息都会创建新的协作组并继承上一组的空闲子实例；授权如果跟随协作组，同一对话中每一轮都会重复询问。
- `automation`：创建或编辑自动化时，用户通过同一种审批卡批准一组长期授权；自动化运行时只认这一作用域和本次运行会话内新产生的授权。接入时需注意：目前审批网关只按运行上下文中的一个 `GrantScope` 查询和写入。自动化入口不能直接把作用域设为 `automation`，否则运行中勾选的“本会话允许”会被写成长期授权；需要让网关同时查询 `automation` 作用域与本次运行会话的 `session` 作用域，运行中产生的授权只写入后者，长期授权只由设置表单写入。
- 匹配时同时校验用户、租户、作用域、未过期、未撤销；资源范围必须完全匹配，不做前缀推断。
- 授权只决定“要不要问”，不扩大能力面或资源权限：工具未注册、资源无写权限时，授权不起作用。
- 提供查看与撤销接口；撤销立即对后续调用生效，不影响已执行的调用。

`automation` 授权不按时间定期过期（创建时可选过期时间，默认不过期），而是在授权前提变化时失效：

- 执行 Agent 模板的工具集合、MCP 服务或提示词发生变化。授权保存 `binding_hash`（这些关键配置的哈希），运行时哈希不一致即视为失效；
- MCP 工具风险被调高，或工具参数 schema hash 变化（复用快照恢复中的 schema hash）；
- 所有者角色降低、档位上限调低，或授权涉及资源的写权限被收回；
- 自动化连续 90 天没有运行。

失效的授权不删除，审批改为 ask 并附原因，自动化运行挂起并通知；用户重新批准时更新 `binding_hash`。实现方式：

- `binding_hash` 由 `types.AutomationBinding{AgentID, Tools, MCPServiceIDs, SystemPrompt, OwnerRole, Ceiling}.Hash()` 计算，工具与服务顺序无关。由自动化运行入口计算后放入运行上下文的 `GrantScope`。
- `AgentGrant.LapseAt` 给出失效原因：`changed`（binding 不一致）、`expired`、`idle`（90 天未使用）、`revoked`。前三种通过审批请求的 `grant_lapsed` 字段带到审批卡并显示说明；用户主动撤销的不提示。
- MCP 工具被设为 `Always` 后不再可授权；风险调高为 destructive 后，`session` 授权和按 write 批准的 `automation` 授权都不再生效（目前由“destructive 不可授权”保证；实现 destructive 自动化授权时改由授权的 `risk` 列保证）。schema 变化通过授权键失效。
- 资源写权限收回不需要单独处理：授权只决定要不要问，路由层仍以真实用户校验写权限。

接口：

- `GET /api/v1/agent/grants?session_id=<根会话 ID>`：列出当前用户在该会话中的有效授权。
- `DELETE /api/v1/agent/grants/:id`：撤销当前用户的一条授权，仅本人可撤销。
- 前端在会话标题的“更多”菜单中提供“已允许的操作”列表与撤销按钮。
- 删除会话时，随会话清理其 `session` 作用域的授权。

### 2.5 委派

- 删除 `Delegation.RequireApproval` 的一刀切规则和协作工具过滤里的档位分支。子任务以父任务的生效档位和根会话的 `session` 授权调用同一个 `Decide`。
- 模板子任务的档位 = min（父任务生效档位，模板 Agent 的档位上限）。由助手发起（`AssistantParent`）或继承了助手配置的子任务，再受工作空间助手上限约束。
- 模板子任务的写工具同样用 `ConfirmedTool` 包装，因此只读档位下的写调用以工具结果形式返回拒绝原因，而不是报错中断子任务。
- 授权作用域放在运行上下文中（`types.WithGrantScope`），子任务继承父任务的根会话作用域；顶层运行未设置时取请求的 `GrantSessionID`，否则取本会话。
- 子任务的 `ask` 仍由现有机制转发到父会话展示，审批归属、恢复、取消不变。
- 父任务对已委派工作的 MCP 防重复检查（`delegated_call_in_progress`）保留，与本模型无关。

### 2.6 身份

`Principal` 增加可选字段 `ActingVia`，例如 `automation:<id>`：

- 路由层仍按该 Web 用户鉴权，operations 可以使用；
- `StorageID()` 不包含 `ActingVia`，因此授权、会话归属和 HITL 的 `UserID` 都仍属于该用户，用户本人可以回答挂起的审批；
- operations 派发时 `AuditAgentAction.ActingVia` 随请求带过去（派发会重建身份，不能依赖原 `Principal`）；`types.ActingViaFromContext` 先取 `AuditAgentAction`，再取 `Principal`；
- 知识库活动审计在 `details.agent_acting_via` 中记录它，区分本人操作与代为执行。

### 2.7 审计

每次 `Decide` 的非 read 结果由 `policy.Record` 写结构化日志（`agent policy decision`），字段为 `agent_action`、`agent_risk`、`agent_always`、`agent_level`、`agent_mode`、`agent_verdict`，代为执行时另有 `agent_acting_via`。命中授权与产生审批另由审批网关记录日志。执行写操作时按现有审计日志记录。

## 3. 兼容与迁移

| 现有数据或行为 | 迁移 |
|---|---|
| `mcp_tool_approvals.require_approval` | 保留列，语义为 `Always`；新增 `risk` 列，可空，空值按注解推断 |
| 普通 Agent 的 MCP 行为 | 默认档位上限 `auto_write`，行为不变 |
| 助手的 operations、Wiki 写工具 | 档位规则不变 |
| 助手 `read_only` 下的 MCP 写工具 | 行为变化：不再注册（修正） |
| 协作子任务的只读 MCP | 行为变化：不再强制审批（修正） |
| 协作子任务的写类 MCP | 规则与父任务一致；父任务已有会话授权时子任务可直接执行 |
| 已有 `AllowInSession` 审批 | 不兼容：功能尚未上线，旧的会话内授权不迁移，用户再次批准即可 |
| `WithUnattendedRun`、`Interactive` 等 | 已删除，运行入口直接设置运行模式（`types.WithRunMode`，`QARequest.Mode`）；未设置时按 `unattended` 处理 |

## 4. 实施顺序

1. **行为不变的收敛**（已完成，`b2719331e`）：新增 `internal/agent/policy`，含 `Risk`（`operations.Risk` 改为其别名）、`Action`、`Rules`、`Decide`、`Offered`。operations 审批、`ConfirmedTool`、MCP `Gate.NeedsApproval`、协作委托的 `RequireApproval` 与子任务工具过滤都改为调用 `Decide`，规则完全复刻现状：MCP 暂按 `auto_write` 档位加 `Always`（租户开关）与 `ForceAsk`（委托）判断；`read_only` 下仍到达的写操作返回 ask 而非 deny。现有测试未改断言全部通过。
2. **规则修正与运行模式**（已完成，运行模式 `e97cd5bfb`，其余 `a65b2fde6`）：MCP 风险分级与注解默认值；`read_only` 覆盖 MCP（注册阶段移除，`Decide` 增加 deny）；删除委派一刀切；普通 Agent 档位上限配置；合并运行模式。运行模式从第 1 步移到这里，因为合并必然改变现有行为：非交互运行中，operations 与 `ConfirmedTool` 直接拒绝，MCP 审批却发出审批卡并等待至超时（IM 渠道即如此）；`WithUnattendedRun` 目前没有生产调用方。
3. **授权统一**（已完成，`a249d8a2d`）：授权表、按根会话匹配的 `session` 作用域、审批卡“本会话允许”扩展到 MCP、查看与撤销界面。
4. **身份与自动化接入**（已完成，`2e875cca4`）：`ActingVia`、决策日志、`automation` 作用域的 binding 与失效原因。`deferred` 模式在第 2 步已实现。自动化需求只需在运行入口设置模式、`GrantScope` 与 `Principal.ActingVia`。
5. **自动化授权接入**：后端已完成——授权表 `risk` 列；`policy.Pregrantable`（write 与 destructive，非 `Always`）决定是否生成授权键，`policy.Grantable`（仅 write）决定审批卡是否提供“本会话允许”，`policy.Covers` 保证授权不覆盖风险更高的调用；网关先查运行的 `GrantScope`，再查 `types.WithStandingGrantScope` 设置的长期作用域，运行中的批准只写入前者。自动化设置表单写入长期授权的接口与界面随自动化需求实现。

## 5. 验收场景

- 第一步完成后，现有所有审批相关测试不改断言即通过。
- 助手 `read_only`：MCP 写工具、Wiki 写工具、operations 写操作均不出现在工具列表中；MCP 只读工具可用。
- 助手 `confirm`：写类 MCP 弹审批卡，选择“本会话允许”后同会话同一工具同一资源范围不再询问，换资源范围再次询问；destructive 每次询问。
- 普通 Agent 未改配置：MCP 审批行为与改造前完全一致。
- 协作：父任务批准“本会话允许”后，子任务调用同一工具同一范围不再弹卡；子任务上批准的授权对父任务和兄弟任务同样生效；用户发下一条消息后（新协作组）续跑的子任务仍沿用这些授权；子任务调用只读 MCP 不弹卡；模板子任务的档位不超过父任务。
- 自动化授权：执行模板的工具列表被他人修改后，下一次运行在对应调用处挂起并说明失效原因；重新批准后恢复；所有者降级或资源写权限收回后同样失效。
- `Always` 的工具（含 `RequireApproval=true` 的 MCP、技能安装）在任何档位和授权下都询问。
- destructive：对话中每次询问，审批卡不提供“本会话允许”；自动化中只有在设置里针对具体资源预先批准过才直接执行，换一个知识库仍挂起询问；工具从 write 调高为 destructive 后，原有 write 授权不再生效。
- MCP 服务把工具注解从 read 改为 write，下一次调用立即按 write 处理；反向变化不生效，需管理员显式设为 read。
- `unattended` 模式下 ask 直接拒绝并告诉模型原因；`deferred` 模式下挂起并产生通知。
- 撤销授权后下一次调用重新询问；已执行的调用不受影响。
- 审计日志中能区分用户本人与 `ActingVia` 的执行。
- 授权不能让未注册的工具出现，也不能让无写权限的知识库变为可写。

## 6. 待定问题

- MCP 工具风险的管理界面放在现有 MCP 服务的工具列表中（`McpToolsList.vue`），还是单独的策略页。暂缓：后端已支持，`PUT /api/v1/mcp-services/:id/tool-approvals/:tool_name` 接受 `risk`（空字符串表示改回按注解推断），`GET` 返回显式配置的 `risk` 与 `observed_risk`；界面后续再定。

已决定：不设协作作用域，授权按根会话匹配（§2.4）；`automation` 授权按前提变化失效，不按时间定期重新确认（§2.4）；不新增“全部直接通过”的档位，`auto_write` 是最高档，destructive 只能通过自动化作用域按具体资源预先授权，`Always` 永不豁免（§2.4）。理由：`Always` 是管理员对具体工具的决定，用户档位不应覆盖它；Agent 读取的知识库、网页和 MCP 结果都可能带有注入指令，全开档位会让这类内容直接触发不可撤销的操作；需要放宽的单个 MCP 工具，管理员可以把它的风险调为 write。

## 7. 实现与原设计的差异

- MCP 注解只采用 `readOnlyHint`；destructive 只能由管理员设置；降级确认改为 `observed_risk` 只升不降（§2.1）。
- `read_only` 下仍到达的写操作返回 deny（§2.2）。
- `Decide` 保持为纯函数 `Decide(Action, Rules) Verdict`，`Action` 只含 `Risk` 与 `Always`；授权键由各工具生成并在审批网关匹配，而不是放进 `Action`。授权在运行模式检查之前匹配，因此 `unattended` 与 `deferred` 运行同样认可已有授权。
- 授权表字段命名为 `grant_key`、`prompt_id`，另有 `label`、`updated_at`（§2.4）。
- 旧的 `AllowInSession` 数据不兼容（§3）。
- 模板子任务的写工具改为包装执行，只读档位的拒绝以工具结果返回（§2.5）。

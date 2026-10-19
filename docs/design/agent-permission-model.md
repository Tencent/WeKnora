# Agent 执行权限模型

状态：设计稿，未实现。日期：2026-10-19。

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
| MCP 工具 | 管理员逐工具配置；未配置时取注解：`readOnlyHint=true` → read，`destructiveHint=true` → destructive，其余 → write | `RequireApproval=true` 映射为 `Always` |

MCP 注解来自外部服务，只用作默认值。管理员的显式配置优先；注解从 read 变为非 read 时立即生效，从非 read 变为 read 时需要管理员确认，避免外部服务自行降低风险。

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
- `read_only` 在注册阶段移除写工具，包括 MCP 写工具和 Wiki 写工具，而不是注册后再拒绝。

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
| `id, tenant_id, user_id` | 授权属于用户本人 |
| `action_key` | `Kind + ID + Resource` |
| `scope` | `session` / `automation` |
| `scope_id` | 根会话 ID 或自动化 ID |
| `source_prompt_id` | 产生该授权的审批 |
| `binding_hash` | 仅 `automation`：授权前提的哈希，见下文失效规则 |
| `expires_at, revoked_at, created_at, last_used_at` | |

规则：

- 只有 `Risk = write` 且 `Always = false` 的动作可以被授权豁免；destructive 和 `Always` 永不豁免。
- `session`：审批卡上的“本会话内允许”，扩展到 MCP 写工具。`scope_id` 始终是**根会话** ID：根任务和委派子任务上批准的授权都写到根会话名下，子任务判断时按根会话匹配。授权随根会话结束，用户删除会话时一并清理。不设单独的协作作用域，原因是每次用户发新消息都会创建新的协作组并继承上一组的空闲子实例；授权如果跟随协作组，同一对话中每一轮都会重复询问。
- `automation`：创建或编辑自动化时，用户通过同一种审批卡批准一组长期授权；自动化运行时只认这一作用域和本次运行会话内新产生的授权。
- 匹配时同时校验用户、租户、作用域、未过期、未撤销；资源范围必须完全匹配，不做前缀推断。
- 授权只决定“要不要问”，不扩大能力面或资源权限：工具未注册、资源无写权限时，授权不起作用。
- 提供查看与撤销接口；撤销立即对后续调用生效，不影响已执行的调用。

`automation` 授权不按时间定期过期（创建时可选过期时间，默认不过期），而是在授权前提变化时失效：

- 执行 Agent 模板的工具集合、MCP 服务或提示词发生变化。授权保存 `binding_hash`（这些关键配置的哈希），运行时哈希不一致即视为失效；
- MCP 工具风险被调高，或工具参数 schema hash 变化（复用快照恢复中的 schema hash）；
- 所有者角色降低、档位上限调低，或授权涉及资源的写权限被收回；
- 自动化连续 90 天没有运行。

失效的授权不删除，`Decide` 对其返回 ask 并附原因（例如“模板 X 的工具列表已变化”），自动化运行挂起并通知；用户重新批准时更新 `binding_hash`。
- 匹配时同时校验用户、租户、作用域、未过期、未撤销；资源范围必须完全匹配，不做前缀推断。
- 授权只决定“要不要问”，不扩大能力面或资源权限：工具未注册、资源无写权限时，授权不起作用。
- 提供查看与撤销接口；撤销立即对后续调用生效，不影响已执行的调用。

### 2.5 委派

- 删除 `Delegation.RequireApproval` 的一刀切规则和协作工具过滤里的档位分支。子任务以父任务的生效档位和根会话的 `session` 授权调用同一个 `Decide`。
- 模板子任务的档位 = min（父任务生效档位，模板 Agent 的档位上限）。
- 子任务的 `ask` 仍由现有机制转发到父会话展示，审批归属、恢复、取消不变。
- 父任务对已委派工作的 MCP 防重复检查（`delegated_call_in_progress`）保留，与本模型无关。

### 2.6 身份

`Principal` 增加可选字段 `ActingVia`，例如 `automation:<id>`：

- 路由层仍按该 Web 用户鉴权，operations 可以使用；
- 审计日志（`auditLogService.Log`）记录 `ActingVia`，区分本人操作与代为执行；
- HITL 的 `UserID` 仍是该用户的 `StorageID()`，用户本人可以回答挂起的审批。

### 2.7 审计

每次 `Decide` 的非 read 结果写结构化日志：动作键、风险、档位、模式、结论、命中的授权或产生的审批 ID。执行写操作时按现有审计日志记录。

## 3. 兼容与迁移

| 现有数据或行为 | 迁移 |
|---|---|
| `mcp_tool_approvals.require_approval` | 保留列，语义为 `Always`；新增 `risk` 列，可空，空值按注解推断 |
| 普通 Agent 的 MCP 行为 | 默认档位上限 `auto_write`，行为不变 |
| 助手的 operations、Wiki 写工具 | 档位规则不变 |
| 助手 `read_only` 下的 MCP 写工具 | 行为变化：不再注册（修正） |
| 协作子任务的只读 MCP | 行为变化：不再强制审批（修正） |
| 协作子任务的写类 MCP | 规则与父任务一致；父任务已有会话授权时子任务可直接执行 |
| 已有 `AllowInSession` 审批 | 读取时兼容旧数据，新授权写入授权表 |
| `WithUnattendedRun`、`Interactive` 等 | 运行入口改为设置运行模式；旧标记在过渡期由模式派生，最终删除 |

## 4. 实施顺序

1. **行为不变的收敛**（已完成）：新增 `internal/agent/policy`，含 `Risk`（`operations.Risk` 改为其别名）、`Action`、`Rules`、`Decide`、`Offered`。operations 审批、`ConfirmedTool`、MCP `Gate.NeedsApproval`、协作委托的 `RequireApproval` 与子任务工具过滤都改为调用 `Decide`，规则完全复刻现状：MCP 暂按 `auto_write` 档位加 `Always`（租户开关）与 `ForceAsk`（委托）判断；`read_only` 下仍到达的写操作返回 ask 而非 deny。现有测试未改断言全部通过。
2. **规则修正与运行模式**：MCP 风险分级与注解默认值；`read_only` 覆盖 MCP（注册阶段移除，`Decide` 增加 deny）；删除委派一刀切；普通 Agent 档位上限配置；合并运行模式。运行模式从第 1 步移到这里，因为合并必然改变现有行为：非交互运行中，operations 与 `ConfirmedTool` 直接拒绝，MCP 审批却发出审批卡并等待至超时（IM 渠道即如此）；`WithUnattendedRun` 目前没有生产调用方。
3. **授权统一**：授权表、按根会话匹配的 `session` 作用域、审批卡“本会话允许”扩展到 MCP、查看与撤销界面。
4. **身份与自动化接入**：`ActingVia`、`deferred` 模式、`automation` 作用域授权，由自动化需求使用。

## 5. 验收场景

- 第一步完成后，现有所有审批相关测试不改断言即通过。
- 助手 `read_only`：MCP 写工具、Wiki 写工具、operations 写操作均不出现在工具列表中；MCP 只读工具可用。
- 助手 `confirm`：写类 MCP 弹审批卡，选择“本会话允许”后同会话同一工具同一资源范围不再询问，换资源范围再次询问；destructive 每次询问。
- 普通 Agent 未改配置：MCP 审批行为与改造前完全一致。
- 协作：父任务批准“本会话允许”后，子任务调用同一工具同一范围不再弹卡；子任务上批准的授权对父任务和兄弟任务同样生效；用户发下一条消息后（新协作组）续跑的子任务仍沿用这些授权；子任务调用只读 MCP 不弹卡；模板子任务的档位不超过父任务。
- 自动化授权：执行模板的工具列表被他人修改后，下一次运行在对应调用处挂起并说明失效原因；重新批准后恢复；所有者降级或资源写权限收回后同样失效。
- `Always` 的工具（含 `RequireApproval=true` 的 MCP、技能安装）在任何档位和授权下都询问。
- MCP 服务把工具注解从 read 改为 write，下一次调用立即按 write 处理；反向变化需要管理员确认才生效。
- `unattended` 模式下 ask 直接拒绝并告诉模型原因；`deferred` 模式下挂起并产生通知。
- 撤销授权后下一次调用重新询问；已执行的调用不受影响。
- 审计日志中能区分用户本人与 `ActingVia` 的执行。
- 授权不能让未注册的工具出现，也不能让无写权限的知识库变为可写。

## 6. 待定问题

- MCP 工具风险的管理界面放在现有 MCP 服务的工具列表中（`McpToolsList.vue`），还是单独的策略页。暂缓决定，第 2 步实现 MCP 风险分级前确定。

已决定：不设协作作用域，授权按根会话匹配（§2.4）；`automation` 授权按前提变化失效，不按时间定期重新确认（§2.4）。

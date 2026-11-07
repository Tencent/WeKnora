# Agent 自动化（定时任务）

状态：第一版已实现，与设计的差异见 §13。日期：2026-10-19。

权限部分建立在 [Agent 执行权限模型](agent-permission-model.md) 之上：自动化不定义自己的审批策略或白名单，只使用该模型的 `deferred` 运行模式、`automation` 作用域授权和 `ActingVia` 身份。权限模型的前三步是本需求的前置条件。

## 0. 已确认的决策

| 问题 | 决策 |
|---|---|
| 会话形态 | 两种都支持，由用户选择：每次运行新会话（默认），或一个自动化固定一个长期会话 |
| 需要确认的操作 | 运行模式为 `deferred`：挂起等待审批并通知；超过自动化配置的期限后放弃，本次运行记为失败 |
| 首版投递渠道 | 只做站内：运行历史 + 通知中心。IM 主动推送、出站 Webhook、邮件放到后续阶段 |
| 创建入口 | 设置页表单与助手对话创建都做，表单先行；助手创建复用同一服务接口和校验 |

## 1. 目标与边界

用户定义一个“在什么时间、以谁的身份、让哪个 Agent 做什么”的自动化。到点后系统在后台无人值守地运行一轮 Agent 对话，结果持久化，并在站内通知创建者。运行中可以使用多 Agent 协作拆分任务，也可以在遇到需审批的写操作时挂起，等用户处理后继续。

第一版包含：

- 触发方式为 cron 周期和一次性定时，支持时区；另提供手动“立即运行”。
- 执行者固定为内置 WeKnora 助手；需要其他智能体时，在任务提示词中写明委派对象与工作，由助手通过现有协作工具委派。
- 运行历史、运行详情（复用会话详情和子任务查看）、停止运行、暂停或恢复自动化。
- 站内通知：运行成功、失败、等待审批、自动暂停。
- 助手可通过 operations 创建和管理自动化，需要用户确认。

第一版不包含：事件触发（数据源同步完成、新文档、入站 Webhook 等）、IM 或 Webhook 或邮件投递、共享 Agent 跨租户执行、API Key 或 IM 用户作为自动化所有者、自动化之间的依赖编排（DAG）。

## 2. 现有能力与缺口

| 已有实现 | 复用方式 | 缺口 |
|---|---|---|
| `sessionService.AgentQA`，已被协作执行器、IM、MCP Server 在后台直接调用 | 作为一次运行的执行入口 | 各调用方重复实现“建消息对、挂事件流、监听完成、落库”，需要抽出统一的运行服务（见 §6） |
| 协作运行时 `internal/agent/collaboration`、`agent_collaborations` | 自动化的根 Agent 满足父任务条件时可直接 `spawn_agent`；持久化、租约、恢复、子审批转发不变 | 协作要求父任务具有 Web 用户身份，需要“以创建者身份运行”（§5.1） |
| `internal/datasource/scheduler.go`（`robfig/cron`，6 字段，含秒） | 复用表达式解析与校验 | 它在每个实例内存里注册 cron 并同时触发，依赖 Redis TaskID 去重；Lite 没有 Redis，也看不到重启期间错过的触发。自动化改用数据库驱动调度（§4） |
| HITL Broker：等待超时转为 `suspended` 并设置 `ExpiresAt`，reaper 回收过期 | 挂起等待审批 | 需要按自动化配置覆盖过期时间，并在挂起时发站内通知 |
| `Session.Kind`（`assistant`、`agent_delegated`） | 新增 `automation`，不进普通会话列表 | 会话列表和权限检查要识别新 kind |
| `types.WithPrincipal`、运行模式标记 | 构造后台运行身份和运行上下文 | 由权限模型合并为运行模式枚举并增加 `ActingVia`（权限模型 §2.3、§2.6） |
| 消息产物（artifact）与文件卡片 | 运行交付物 | 无 |
| `GlobalInvitationBell.vue` | 仅邀请通知 | 没有通用的站内通知中心，需要新建（§7） |
| 助手 operations 目录 | 新增 automation 系列操作 | 无 |

## 3. 数据模型

### 3.1 `automations`

| 字段 | 说明 |
|---|---|
| `id, tenant_id, owner_user_id` | 所有者是创建者的账户 ID，不保存任何凭据 |
| `name, description, enabled` | |
| `model_id` | 可选，覆盖助手本次执行使用的模型；为空跟随助手默认模型，委派的智能体仍使用各自的模型 |
| `instruction` | 任务提示词，支持运行时变量（§5.4） |
| `trigger` | JSON：`{type: cron, expr, timezone}` 或 `{type: once, at, timezone}` |
| `session_mode` | `per_run`（默认）或 `persistent` |
| `persistent_session_id` | `persistent` 模式下的固定会话，首次运行时创建 |
| `carry_last_result` | `per_run` 模式下是否注入上次成功运行的结果摘要 |
| `misfire_policy` | `skip`（默认）或 `run_once`：停机或暂停期间错过的触发，最多补跑一次 |
| `overlap_policy` | `skip`（默认）或 `queue`：上一次还在运行时如何处理本次触发；`queue` 最多排队一个 |
| `access_level` | 执行档位，语义见权限模型 §2.2；默认 `confirm`，`auto_write` 必须显式选择；运行时仍受工作空间的助手权限上限约束 |
| `approval_timeout` | 挂起等待审批的最长时间，默认 24 小时 |
| `limits` | JSON：单次运行最长时间、最大迭代数、token 上限 |
| `next_run_at, last_run_at, last_success_at` | 调度状态 |
| `consecutive_failures, paused_reason` | 连续失败计数与自动暂停原因 |
| `version` | CAS 版本 |
| `created_at, updated_at, deleted_at` | |

索引：`(enabled, next_run_at)` 供调度扫描；`(tenant_id, owner_user_id)` 供列表。

### 3.2 `automation_runs`

| 字段 | 说明 |
|---|---|
| `id, automation_id, tenant_id` | |
| `scheduled_at` | 计划触发时间；手动运行为发起时间 |
| `trigger_kind` | `schedule` / `manual` / `misfire` |
| `status` | 见 §4.3 |
| `session_id, user_message_id, assistant_message_id` | 本次运行对应的会话与消息 |
| `owner, lease_until` | 执行所有权，与协作运行时同一套 CAS 加租约语义 |
| `pending_prompt_id` | 挂起等待审批时的 prompt |
| `effective_config` | JSON：本次实际使用的 Agent、模型、知识库范围、工具集合摘要，用于排查 |
| `usage, error, started_at, finished_at` | |

唯一约束：`(automation_id, scheduled_at, trigger_kind)`。PostgreSQL 和 SQLite 同时提供迁移，不使用仅 PostgreSQL 支持的锁语法。

保留策略：每个自动化保留最近 N 条运行记录（可配置，默认 200）。清理运行记录时同时清理 `per_run` 模式的会话；`persistent` 会话随自动化删除。

## 4. 调度

### 4.1 数据库驱动的领取

调度器每个实例每隔 tick（默认 15 秒）执行：

1. 查询 `enabled = true AND next_run_at <= now` 的自动化，限制批量大小。
2. 对每条记录在一个事务内：用 `version` 做 CAS，把 `next_run_at` 推进到下一次触发时间；插入一条 `queued` 的运行记录（唯一键冲突说明其他实例已领取，直接跳过）。
3. 提交后投递执行。有 Redis 时走 asynq 新队列 `automation`；Lite 时走进程内执行器。投递只负责唤起，真正的执行权由运行记录上的 CAS 和租约决定。
4. 另有一个扫描任务处理 `queued` 但长时间未被领取的运行记录，避免投递丢失。

这样多副本、Lite、重启三种情况行为一致，不依赖进程内 cron 表，也不依赖 Redis 去重。

### 4.2 时间计算

- 使用 `robfig/cron` 与数据源一致的 6 字段解析器，额外支持 `CRON_TZ=` 前缀或独立的 `timezone` 字段，统一按 IANA 时区计算后存 UTC。
- 校验时计算并返回未来 3 次触发时间，表单与助手都向用户回显。
- 最小触发间隔可由系统设置控制（默认 5 分钟），过密的表达式拒绝。
- 一次性触发运行后自动 `enabled = false`。
- 错过的触发：`skip` 直接推进到未来；`run_once` 插入一条 `trigger_kind = misfire` 的运行，再推进到未来，不逐个补跑。

### 4.3 运行状态

```text
queued → running → succeeded | failed | cancelled
              ↘ awaiting_approval → running
              ↘ needs_attention                      （进程崩溃时执行结果未知）
queued → skipped                                     （重叠策略或自动化已暂停）
```

- `overlap_policy = skip`：领取时发现同一自动化有 `running` 或 `awaiting_approval` 的运行，则新运行记为 `skipped` 并记录原因。
- 进程崩溃恢复与协作一致：`queued` 可重新领取；`awaiting_approval` 由审批回答恢复；普通 `running` 转为 `needs_attention`，不自动重放副作用。
- 单次运行超过 `limits` 中的最长时间，取消执行，记为 `failed`。挂起等待审批的时间不计入执行时长，单独受挂起期限约束。

## 5. 执行

### 5.1 运行身份

- 运行前加载所有者，重新校验：账户有效、仍是工作空间成员、内置助手仍可用、知识库与 MCP 的授权范围仍成立。任一失败则本次 `failed`，并将自动化暂停、写入 `paused_reason`、发通知。
- 校验通过后构造上下文：`Principal{web_user, owner_user_id, ActingVia: "automation:<id>"}`，租户与工作空间取自自动化记录。路由层按所有者鉴权，因此 operations 可用；根 Agent 满足协作父任务条件，可以 `spawn_agent`。审计日志通过 `ActingVia` 区分代为执行。
- 子任务按权限模型 §2.5 处理：档位不超过父任务，继承本次运行的授权。

### 5.2 运行模式与授权

- 运行模式为 `deferred`（权限模型 §2.3）：不注册 `ask_user`；需要 OAuth 的 MCP 返回错误，模型在结果中说明；信息不足时由模型在交付物中写明缺失项。
- 每次调用由权限模型的 `Decide` 判断，自动化只提供两项输入：执行档位 `access_level`，以及 `automation` 作用域的长期授权。
- 长期授权在创建或编辑自动化时产生：用户勾选“运行时无需确认”的写类工具及资源范围，通过与对话中相同的审批卡批准，写入授权表（权限模型 §2.4）。destructive 工具可以预先授权，但必须指定具体资源范围（例如某个知识库），在审批卡上与写类授权分开列出并标明不可撤销；`Always` 工具不能预先授权（权限模型 §2.4）。
- `Decide` 返回 ask 时：照常进入 HITL，挂起运行，过期时间取 `approval_timeout`；运行记为 `awaiting_approval`，发站内通知，用户在运行详情（即自动化会话）里处理。审批卡上选择“本会话允许”时写入会话授权；如需对以后的运行也生效，在自动化设置里添加长期授权。
- 子任务的 ask 通过协作已有的审批转发展示在自动化会话中，规则相同。
- 所有者撤销长期授权或降低档位后，下一次调用立即生效。
- 系统提示词追加一段自动化运行说明：当前没有用户在线；触发时间与运行编号；必须产出完整交付物；不要询问用户；遇到阻塞时如实说明。该说明通过运行时通知注入，不写进 Agent 模板。

### 5.3 会话形态

- `per_run`：每次运行新建 `kind = automation` 的会话，标题为“自动化名 · 触发时间”。`carry_last_result = true` 时，把上次成功运行的最终回答（按长度上限截断并标注）作为背景材料放进本次用户消息。跨运行不继承协作子实例。
- `persistent`：所有运行写入同一会话，每次追加一对消息，沿用现有多轮历史与上下文压缩；同一会话上的执行由现有会话级互斥保证不并发；如果用户正在这个会话里手动对话，本次触发按 `overlap_policy` 跳过或排队。适合“和上次对比”这类场景，但上下文会持续增长，表单上需提示。
- 两种模式的会话都不进普通会话列表，从自动化详情进入。

### 5.4 运行时变量

`instruction` 支持以下变量，运行时替换后作为用户消息：

- `{{now}}`：本次触发时间（自动化时区）
- `{{last_run_at}}`、`{{last_success_at}}`：上次运行、上次成功时间；首次运行为空并给出说明
- `{{run_id}}`、`{{automation_name}}`

变量只做文本替换，不执行表达式。

### 5.5 护栏

- 每租户自动化数量上限、全局并发执行上限（与协作子任务的全局并发上限分开计数）。
- 单次运行的 token、迭代次数与时长上限；超出即结束并记录。
- 连续失败达到阈值（默认 3 次）自动暂停并通知。
- 删除自动化时先停止进行中的运行，再按保留规则清理。

## 6. 统一运行服务

协作执行器、IM、MCP Server 现在各自实现了“创建用户与助手消息、挂事件流和实时运行标记、监听完成与错误事件、落库、清理挂起快照”。自动化是第四个后台调用方。实现自动化时先抽出 `AgentRunService`：

- `Start(ctx, RunSpec)`：RunSpec 包含会话、查询、Agent 快照、运行模式、执行档位、身份（含 `ActingVia`）、审批委托钩子、限制；返回运行结果（最终回答、产物、usage、是否挂起）。
- `Resume(ctx, prompt)`：审批恢复。
- `Interrupt(ctx, session, message)`：停止。

协作执行器先迁移到该服务，以现有协作回归测试作为不回退的依据；IM 与 MCP Server 的迁移不在本需求范围内，可以后续单独做。

## 7. 站内通知中心

新增通用通知模型，不和邀请混用：

- `notifications`：`id, tenant_id, user_id, kind, title, body, link, source_type, source_id, read_at, created_at`。
- 首批类型：`automation.succeeded`、`automation.failed`、`automation.awaiting_approval`、`automation.paused`。
- 每个自动化可配置“成功是否通知”（默认关闭，仅失败、等待审批、暂停时通知），避免高频任务刷屏。
- 前端在顶栏铃铛合并展示邀请与通知；实时推送可先用轮询，后续再接 SSE。

后续增加 IM、Webhook、邮件时，作为通知的其他投递方式扩展，不改动运行模型。IM 需要先给 Adapter 增加可选的主动发送接口，现有 `SendReply` 必须基于一条收到的消息。

## 8. 接口与前端

API（均校验所有者或空间管理员权限）：

- `GET/POST /automations`，`GET/PUT/DELETE /automations/:id`
- `POST /automations/:id/pause`、`/resume`、`/run`（立即运行）
- `POST /automations/validate-trigger`：返回是否合法及未来 3 次触发时间
- `GET /automations/:id/runs`、`GET /automation-runs/:id`、`POST /automation-runs/:id/cancel`
- `GET /notifications`、`POST /notifications/read`

前端：

- 自动化列表：名称、触发描述、下次运行、最近状态、启用开关。
- 编辑表单：运行模型、任务提示词（含变量提示）、触发方式（预设项“每天/每周/每月”加高级 cron）、时区、会话形态、执行档位、长期授权（勾选运行时无需确认的写类与 destructive 工具及资源范围；destructive 项单独分组、必须选择具体资源，保存时弹审批卡）、审批等待期限、限制、通知选项。
- 运行历史与详情：详情直接打开自动化会话，复用现有消息流、子任务卡片、审批卡和产物抽屉。

## 9. 助手集成

operations 新增：`automation.list`、`automation.get`、`automation.create`、`automation.update`、`automation.pause`、`automation.resume`、`automation.run_now`、`automation.delete`。

- 写操作走现有 confirm 审批，审批卡展示人类可读的触发描述、未来 3 次触发时间、执行档位和将要创建的长期授权。长期授权由用户在审批卡上批准，模型不能代为授予。
- 模型负责把自然语言转换为 cron 和时区，服务端负责校验；模型不能设置所有者，所有者固定为当前用户。
- 助手只读档位下只能列出和查看。

## 10. 实施顺序

0. **前置**：权限模型第 1–4 步（`Decide` 收敛、规则修正、授权统一、`ActingVia` 与 `deferred` 模式）。
1. **运行基础**：两张表与迁移、`AgentRunService` 抽取并迁移协作执行器、数据库调度器（Redis 与 Lite 两种投递）、运行身份、`automation` 会话 kind、两种会话形态。
2. **管理与可见性**：API、列表/表单/运行历史页面、立即运行与停止、护栏与自动暂停。
3. **通知与审批闭环**：通知中心、挂起审批的通知与过期处理。
4. **助手集成**：operations 与审批卡展示。
5. **后续**：事件触发、IM 主动推送、出站 Webhook、邮件。

## 11. 验收场景

- 两个实例同时扫描到同一到期自动化，只产生一条运行记录、执行一次。
- Lite 重启后，停机期间错过的触发按 `misfire_policy` 处理；`run_once` 只补跑一次。
- 时区为 `Asia/Shanghai` 的“每天 9:00”在 UTC 服务器上按北京时间触发；跨夏令时时区的触发时间正确。
- `overlap_policy = skip` 时，上一次未结束则本次记为 `skipped`。
- 所有者被移出工作空间后，下一次运行失败，自动化暂停并通知；不以旧权限执行。
- 助手不可用时运行失败并给出明确原因；委派目标不可用时由助手报告具体阻塞。
- 命中长期授权的写类工具不弹审批；未命中的挂起并通知，审批后从原轮次继续，只执行一次；超过 `approval_timeout` 后运行失败，迟到的审批不会恢复。
- destructive 工具未在设置中预先授权时，运行时每次挂起询问，审批卡不提供“以后都允许”；在设置中对知识库 X 预先授权后，删除 X 中的文档直接执行，删除其他知识库中的文档仍挂起询问。
- `Always` 工具（管理员要求每次审批的 MCP、技能安装）在设置中不可勾选，运行时每次挂起询问。
- 撤销长期授权后，下一次运行中同一调用重新挂起询问。
- 自动化运行中 `spawn_agent` 创建子任务，子任务继承本次运行的授权，档位不超过父任务，结果汇总进本次运行。
- 审计日志中自动化执行的写操作带 `ActingVia = automation:<id>`。
- 运行中进程崩溃，运行转为 `needs_attention`，不重放已发生的工具调用。
- `ask_user` 不可用；需要 OAuth 的 MCP 返回错误而不是卡住。
- 超过时长或 token 上限时运行结束并记为失败；连续失败 3 次自动暂停。
- `per_run` 与 `carry_last_result` 正确注入上次结果；`persistent` 模式多次运行写入同一会话，历史连续。
- 自动化会话不出现在普通会话列表，其他用户无法访问。
- 助手创建自动化时展示的触发时间与服务端计算一致；只读档位下无法创建。
- 协作执行器迁移到 `AgentRunService` 后，现有协作、恢复、审批回归测试全部通过。

## 12. 待定问题

- 共享空间里，空间管理员能否查看或停用成员创建的自动化。
- `persistent` 会话长期运行后的上下文压缩策略是否需要比普通会话更激进。
- 通知中心是否需要按工作空间隔离展示。
- 每租户数量与并发上限的默认值，以及是否放进系统设置页。

## 13. 实现说明

第一版按 §10 的第 1–4 步实现，以下几处与设计不同：

- **没有抽出 `AgentRunService`**。执行器 `automation_runner.go` 照协作执行器的写法自己完成建消息对、挂事件流、监听完成与错误、落库；协作执行器不动。四个后台调用方的统一留作单独的重构。
- **只有所有者能访问**。接口按所有者过滤，空间管理员不能查看或停用他人的自动化（§12 第一条仍待定）。API Key、IM 用户和自动化运行内部（`ActingVia` 非空）都不能创建或管理自动化。
- **调度只走数据库**。所有实例每 15 秒扫描到期记录，用 `version` 做 CAS 领取，不经 Redis 投递；Lite 与多实例部署同一套逻辑。
- **审批过期由调度器对账**，不改 HITL Broker 的过期时间：等待审批超过 `approval_timeout_minutes` 后运行记为失败并关闭提示。Broker 对挂起提示的回收如果更早，运行同样记为失败，原因取提示的关闭原因。
- **长期授权从历史审批提升**。设置页不提供工具清单勾选，而是列出本自动化运行中请求过审批、尚未授权的写类与 destructive 调用，由所有者逐条确认；授权绑定 Agent 配置与所有者角色（`AutomationBinding`），变化后显示失效。需要每次审批的调用不会出现在候选里。
- **限制只有时长**。`limits` 目前只有 `max_duration_minutes`（默认 30，上限 240，不含等待审批的时间）；token 上限没有单独实现，由 Agent 自身的 `max_iterations` 约束。
- **`persistent` 会话忙时跳过**。长期会话里还有进行中的运行（包括用户在该会话里手动对话）时，本次运行记为 `skipped`。
- **执行入口固定为内置助手**（`builtin-weknora-assistant`），自动化表与创建、更新接口不提供 `agent_id`。表单和对话创建使用同一规则，提示词可要求助手委派其他智能体。
- **前端入口**：侧栏“自动化”页面（`/platform/automations`），运行详情直接打开自动化会话；通知链接 `/platform/automations/:id?run=:runId` 打开对应运行。右上角铃铛合并展示邀请与通知，60 秒轮询。
- **助手审批卡**仍是通用的 operation 参数展示，未单独渲染触发描述与未来触发时间；助手需要先调用 `automation.validate_trigger` 并在对话里告知用户。
